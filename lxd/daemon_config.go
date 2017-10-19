package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	log "github.com/lxc/lxd/shared/log15"

	"github.com/lxc/lxd/lxd/cluster"
	dbapi "github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/state"
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

func daemonConfigInit(cluster *dbapi.Cluster) error {
	// Set all the keys
	daemonConfig = map[string]*daemonConfigKey{
		"core.proxy_http":         {valueType: "string"},
		"core.proxy_https":        {valueType: "string"},
		"core.proxy_ignore_hosts": {valueType: "string"},
		"core.trust_password":     {valueType: "string", hiddenValue: true},
		"core.macaroon.endpoint":  {valueType: "string"},

		"images.auto_update_cached":    {valueType: "bool", defaultValue: "true"},
		"images.auto_update_interval":  {valueType: "int", defaultValue: "6"},
		"images.compression_algorithm": {valueType: "string", defaultValue: "gzip"},
		"images.remote_cache_expiry":   {valueType: "int", defaultValue: "10"},

		"maas.api.key": {valueType: "string", setter: daemonConfigSetMAAS},
		"maas.api.url": {valueType: "string", setter: daemonConfigSetMAAS},
		"maas.machine": {valueType: "string", setter: daemonConfigSetMAAS},
	}

	// Load the values from the DB
	var dbValues map[string]string
	err := cluster.Transaction(func(tx *dbapi.ClusterTx) error {
		var err error
		dbValues, err = tx.Config()
		return err
	})
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

func daemonConfigRender(state *state.State) (map[string]interface{}, error) {
	config := map[string]interface{}{}

	// Turn the config into a JSON-compatible map
	err := state.Cluster.Transaction(func(tx *dbapi.ClusterTx) error {
		clusterConfig, err := cluster.ConfigLoad(tx)
		if err != nil {
			return err
		}
		for key, value := range clusterConfig.Dump() {
			config[key] = value
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = state.Node.Transaction(func(tx *dbapi.NodeTx) error {
		nodeConfig, err := node.ConfigLoad(tx)
		if err != nil {
			return err
		}
		for key, value := range nodeConfig.Dump() {
			config[key] = value
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return config, nil
}

func daemonConfigSetProxy(d *Daemon, config map[string]string) {
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
