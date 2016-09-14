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

	"golang.org/x/crypto/scrypt"
	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/shared"
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
	// No need to validate when unsetting
	if value == "" {
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

	err = dbConfigValueSet(d.db, name, value)
	if err != nil {
		return err
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
		"core.https_address":             &daemonConfigKey{valueType: "string", setter: daemonConfigSetAddress},
		"core.https_allowed_headers":     &daemonConfigKey{valueType: "string"},
		"core.https_allowed_methods":     &daemonConfigKey{valueType: "string"},
		"core.https_allowed_origin":      &daemonConfigKey{valueType: "string"},
		"core.https_allowed_credentials": &daemonConfigKey{valueType: "bool"},
		"core.proxy_http":                &daemonConfigKey{valueType: "string", setter: daemonConfigSetProxy},
		"core.proxy_https":               &daemonConfigKey{valueType: "string", setter: daemonConfigSetProxy},
		"core.proxy_ignore_hosts":        &daemonConfigKey{valueType: "string", setter: daemonConfigSetProxy},
		"core.trust_password":            &daemonConfigKey{valueType: "string", hiddenValue: true, setter: daemonConfigSetPassword},

		"images.auto_update_cached":    &daemonConfigKey{valueType: "bool", defaultValue: "true"},
		"images.auto_update_interval":  &daemonConfigKey{valueType: "int", defaultValue: "6"},
		"images.compression_algorithm": &daemonConfigKey{valueType: "string", validator: daemonConfigValidateCompression, defaultValue: "gzip"},
		"images.remote_cache_expiry":   &daemonConfigKey{valueType: "int", defaultValue: "10", trigger: daemonConfigTriggerExpiry},

		"storage.lvm_fstype":           &daemonConfigKey{valueType: "string", defaultValue: "ext4", validValues: []string{"ext4", "xfs"}},
		"storage.lvm_mount_options":    &daemonConfigKey{valueType: "string", defaultValue: "discard"},
		"storage.lvm_thinpool_name":    &daemonConfigKey{valueType: "string", defaultValue: "LXDPool", validator: storageLVMValidateThinPoolName},
		"storage.lvm_vg_name":          &daemonConfigKey{valueType: "string", validator: storageLVMValidateVolumeGroupName, setter: daemonConfigSetStorage},
		"storage.lvm_volume_size":      &daemonConfigKey{valueType: "string", defaultValue: "10GiB"},
		"storage.zfs_pool_name":        &daemonConfigKey{valueType: "string", validator: storageZFSValidatePoolName, setter: daemonConfigSetStorage},
		"storage.zfs_remove_snapshots": &daemonConfigKey{valueType: "bool"},
		"storage.zfs_use_refquota":     &daemonConfigKey{valueType: "bool"},
	}

	// Load the values from the DB
	dbValues, err := dbConfigValuesGet(db)
	if err != nil {
		return err
	}

	daemonConfigLock.Lock()
	for k, v := range dbValues {
		_, ok := daemonConfig[k]
		if !ok {
			shared.LogError("Found invalid configuration key in database", log.Ctx{"key": k})
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

func daemonConfigSetStorage(d *Daemon, key string, value string) (string, error) {
	// The storage driver looks at daemonConfig so just set it temporarily
	daemonConfigLock.Lock()
	oldValue := daemonConfig[key].Get()
	daemonConfig[key].currentValue = value
	daemonConfigLock.Unlock()

	defer func() {
		daemonConfigLock.Lock()
		daemonConfig[key].currentValue = oldValue
		daemonConfigLock.Unlock()
	}()

	// Update the current storage driver
	err := d.SetupStorageDriver()
	if err != nil {
		return "", err
	}

	return value, nil
}

func daemonConfigSetAddress(d *Daemon, key string, value string) (string, error) {
	// Update the current https address
	err := d.UpdateHTTPsPort(value)
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
	for k, _ := range imageStreamCache {
		delete(imageStreamCache, k)
	}
	imageStreamCacheLock.Unlock()

	return value, nil
}

func daemonConfigTriggerExpiry(d *Daemon, key string, value string) {
	// Trigger an image pruning run
	d.pruneChan <- true
}

func daemonConfigValidateCompression(d *Daemon, key string, value string) error {
	if value == "none" {
		return nil
	}

	_, err := exec.LookPath(value)
	return err
}
