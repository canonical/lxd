package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	log "github.com/lxc/lxd/shared/log15"
	"golang.org/x/crypto/scrypt"

	dbapi "github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

var daemonConfigLock sync.Mutex
var daemonConfig map[string]*daemonConfigKey

type daemonConfigKey struct {
	valueType    string
	defaultValue string
	validValues  []string
	currentValue string
	hiddenValue  bool

	validator func(d *Daemon, key string, value string) error
	setter    func(d *Daemon, key string, value string) (string, error)
	trigger   func(d *Daemon, key string, value string)
}

func (k *daemonConfigKey) name() string {
	name := ""

	// Look for a matching entry in daemonConfig
	daemonConfigLock.Lock()
	for key, value := range daemonConfig {
		if value == k {
			name = key
			break
		}
	}
	daemonConfigLock.Unlock()

	return name
}

func (k *daemonConfigKey) Validate(d *Daemon, value string) error {
	// Handle unsetting
	if value == "" {
		value = k.defaultValue

		if k.validator != nil {
			err := k.validator(d, k.name(), value)
			if err != nil {
				return err
			}
		}

		return nil
	}

	// Validate booleans
	if k.valueType == "bool" && !shared.StringInSlice(strings.ToLower(value), []string{"true", "false", "1", "0", "yes", "no", "on", "off"}) {
		return fmt.Errorf("Invalid value for a boolean: %s", value)
	}

	// Validate integers
	if k.valueType == "int" {
		_, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
	}

	// Check against valid values
	if k.validValues != nil && !shared.StringInSlice(value, k.validValues) {
		return fmt.Errorf("Invalid value, only the following values are allowed: %s", k.validValues)
	}

	// Run external validation function
	if k.validator != nil {
		err := k.validator(d, k.name(), value)
		if err != nil {
			return err
		}
	}

	return nil
}

func (k *daemonConfigKey) Set(d *Daemon, value string) error {
	var name string

	// Check if we are actually changing things
	oldValue := k.currentValue
	if oldValue == value {
		return nil
	}

	// Validate the new value
	err := k.Validate(d, value)
	if err != nil {
		return err
	}

	// Run external setting function
	if k.setter != nil {
		value, err = k.setter(d, k.name(), value)
		if err != nil {
			return err
		}
	}

	// Get the configuration key and make sure daemonConfig is sane
	name = k.name()
	if name == "" {
		return fmt.Errorf("Corrupted configuration cache")
	}

	// Actually apply the change
	daemonConfigLock.Lock()
	k.currentValue = value
	daemonConfigLock.Unlock()

	err = dbapi.ConfigValueSet(d.db.DB(), name, value)
	if err != nil {
		return err
	}

	// Run the trigger (if any)
	if k.trigger != nil {
		k.trigger(d, k.name(), value)
	}

	return nil
}

func (k *daemonConfigKey) Get() string {
	value := k.currentValue

	// Get the default value if not set
	if value == "" {
		value = k.defaultValue
	}

	return value
}

func (k *daemonConfigKey) GetBool() bool {
	value := k.currentValue

	// Get the default value if not set
	if value == "" {
		value = k.defaultValue
	}

	// Convert to boolean
	return shared.IsTrue(value)
}

func (k *daemonConfigKey) GetInt64() int64 {
	value := k.currentValue

	// Get the default value if not set
	if value == "" {
		value = k.defaultValue
	}

	// Convert to int64
	ret, _ := strconv.ParseInt(value, 10, 64)
	return ret
}

func daemonConfigInit(db *sql.DB) error {
	// Set all the keys
	daemonConfig = map[string]*daemonConfigKey{
		"core.https_address":             {valueType: "string", setter: daemonConfigSetAddress},
		"core.https_allowed_headers":     {valueType: "string"},
		"core.https_allowed_methods":     {valueType: "string"},
		"core.https_allowed_origin":      {valueType: "string"},
		"core.https_allowed_credentials": {valueType: "bool"},
		"core.proxy_http":                {valueType: "string", setter: daemonConfigSetProxy},
		"core.proxy_https":               {valueType: "string", setter: daemonConfigSetProxy},
		"core.proxy_ignore_hosts":        {valueType: "string", setter: daemonConfigSetProxy},
		"core.trust_password":            {valueType: "string", hiddenValue: true, setter: daemonConfigSetPassword},
		"core.macaroon.endpoint":         {valueType: "string", setter: daemonConfigSetMacaroonEndpoint},

		"images.auto_update_cached":    {valueType: "bool", defaultValue: "true"},
		"images.auto_update_interval":  {valueType: "int", defaultValue: "6", trigger: daemonConfigTriggerAutoUpdateInterval},
		"images.compression_algorithm": {valueType: "string", validator: daemonConfigValidateCompression, defaultValue: "gzip"},
		"images.remote_cache_expiry":   {valueType: "int", defaultValue: "10", trigger: daemonConfigTriggerExpiry},

		"maas.api.key": {valueType: "string", setter: daemonConfigSetMAAS},
		"maas.api.url": {valueType: "string", setter: daemonConfigSetMAAS},
		"maas.machine": {valueType: "string", setter: daemonConfigSetMAAS},

		// Keys deprecated since the implementation of the storage api.
		"storage.lvm_fstype":           {valueType: "string", defaultValue: "ext4", validValues: []string{"btrfs", "ext4", "xfs"}, validator: storageDeprecatedKeys},
		"storage.lvm_mount_options":    {valueType: "string", defaultValue: "discard", validator: storageDeprecatedKeys},
		"storage.lvm_thinpool_name":    {valueType: "string", defaultValue: "LXDPool", validator: storageDeprecatedKeys},
		"storage.lvm_vg_name":          {valueType: "string", validator: storageDeprecatedKeys},
		"storage.lvm_volume_size":      {valueType: "string", defaultValue: "10GiB", validator: storageDeprecatedKeys},
		"storage.zfs_pool_name":        {valueType: "string", validator: storageDeprecatedKeys},
		"storage.zfs_remove_snapshots": {valueType: "bool", validator: storageDeprecatedKeys},
		"storage.zfs_use_refquota":     {valueType: "bool", validator: storageDeprecatedKeys},
	}

	// Load the values from the DB
	dbValues, err := dbapi.ConfigValuesGet(db)
	if err != nil {
		return err
	}

	daemonConfigLock.Lock()
	for k, v := range dbValues {
		_, ok := daemonConfig[k]
		if !ok {
			logger.Error("Found unknown configuration key in database", log.Ctx{"key": k})
			continue
		}

		daemonConfig[k].currentValue = v
	}
	daemonConfigLock.Unlock()

	return nil
}

func daemonConfigRender() map[string]interface{} {
	config := map[string]interface{}{}

	// Turn the config into a JSON-compatible map
	for k, v := range daemonConfig {
		value := v.Get()
		if value != v.defaultValue {
			if v.hiddenValue {
				config[k] = true
			} else {
				config[k] = value
			}
		}
	}

	return config
}

func daemonConfigSetPassword(d *Daemon, key string, value string) (string, error) {
	// Nothing to do on unset
	if value == "" {
		return value, nil
	}

	// Hash the password
	buf := make([]byte, 32)
	_, err := io.ReadFull(rand.Reader, buf)
	if err != nil {
		return "", err
	}

	hash, err := scrypt.Key([]byte(value), buf, 1<<14, 8, 1, 64)
	if err != nil {
		return "", err
	}

	buf = append(buf, hash...)
	value = hex.EncodeToString(buf)

	return value, nil
}

func daemonConfigSetAddress(d *Daemon, key string, value string) (string, error) {
	err := d.endpoints.NetworkUpdateAddress(value)
	if err != nil {
		return "", err
	}

	return value, nil
}

func daemonConfigSetMacaroonEndpoint(d *Daemon, key string, value string) (string, error) {
	err := d.setupExternalAuthentication(value)
	if err != nil {
		return "", err
	}

	return value, nil
}

func daemonConfigSetProxy(d *Daemon, key string, value string) (string, error) {
	// Get the current config
	config := map[string]string{}
	config["core.proxy_https"] = daemonConfig["core.proxy_https"].Get()
	config["core.proxy_http"] = daemonConfig["core.proxy_http"].Get()
	config["core.proxy_ignore_hosts"] = daemonConfig["core.proxy_ignore_hosts"].Get()

	// Apply the change
	config[key] = value

	// Update the cached proxy function
	d.proxy = shared.ProxyFromConfig(
		config["core.proxy_https"],
		config["core.proxy_http"],
		config["core.proxy_ignore_hosts"],
	)

	// Clear the simplestreams cache as it's tied to the old proxy config
	imageStreamCacheLock.Lock()
	for k := range imageStreamCache {
		delete(imageStreamCache, k)
	}
	imageStreamCacheLock.Unlock()

	return value, nil
}

func daemonConfigSetMAAS(d *Daemon, key string, value string) (string, error) {
	maasUrl := daemonConfig["maas.api.url"].Get()
	if key == "maas.api.url" {
		maasUrl = value
	}

	maasKey := daemonConfig["maas.api.key"].Get()
	if key == "maas.api.key" {
		maasKey = value
	}

	maasMachine := daemonConfig["maas.machine"].Get()
	if key == "maas.machine" {
		maasMachine = value
	}

	err := d.setupMAASController(maasUrl, maasKey, maasMachine)
	if err != nil {
		return "", err
	}

	return value, nil
}

func daemonConfigTriggerExpiry(d *Daemon, key string, value string) {
	// Trigger an image pruning run
	d.taskPruneImages.Reset()
}

func daemonConfigTriggerAutoUpdateInterval(d *Daemon, key string, value string) {
	// Reset the auto-update interval loop
	d.taskAutoUpdate.Reset()
}

func daemonConfigValidateCompression(d *Daemon, key string, value string) error {
	if value == "none" {
		return nil
	}

	_, err := exec.LookPath(value)
	return err
}

func storageDeprecatedKeys(d *Daemon, key string, value string) error {
	if value == "" || daemonConfig[key].defaultValue == value {
		return nil
	}

	return fmt.Errorf("Setting the key \"%s\" is deprecated in favor of storage pool configuration.", key)
}
