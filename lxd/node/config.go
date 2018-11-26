package node

import (
	"fmt"

	"github.com/lxc/lxd/lxd/config"
	"github.com/lxc/lxd/lxd/db"
)

// Config holds node-local configuration values for a certain LXD instance.
type Config struct {
	tx *db.NodeTx // DB transaction the values in this config are bound to
	m  config.Map // Low-level map holding the config values.
}

// ConfigLoad loads a new Config object with the current node-local configuration
// values fetched from the database. An optional list of config value triggers
// can be passed, each config key must have at most one trigger.
func ConfigLoad(tx *db.NodeTx) (*Config, error) {
	// Load current raw values from the database, any error is fatal.
	values, err := tx.Config()
	if err != nil {
		return nil, fmt.Errorf("cannot fetch node config from database: %v", err)
	}

	m, err := config.SafeLoad(ConfigSchema, values)
	if err != nil {
		return nil, fmt.Errorf("failed to load node config: %v", err)
	}

	return &Config{tx: tx, m: m}, nil
}

// HTTPSAddress returns the address and port this LXD node should expose its
// API to, if any.
func (c *Config) HTTPSAddress() string {
	return c.m.GetString("core.https_address")
}

// ClusterAddress returns the address and port this LXD node should use for
// cluster communication.
func (c *Config) ClusterAddress() string {
	return c.m.GetString("cluster.https_address")
}

// DebugAddress returns the address and port to setup the pprof listener on
func (c *Config) DebugAddress() string {
	return c.m.GetString("core.debug_address")
}

// MAASMachine returns the MAAS machine this instance is associated with, if
// any.
func (c *Config) MAASMachine() string {
	return c.m.GetString("maas.machine")
}

// Dump current configuration keys and their values. Keys with values matching
// their defaults are omitted.
func (c *Config) Dump() map[string]interface{} {
	return c.m.Dump()
}

// Replace the current configuration with the given values.
func (c *Config) Replace(values map[string]interface{}) (map[string]string, error) {
	return c.update(values)
}

// Patch changes only the configuration keys in the given map.
func (c *Config) Patch(patch map[string]interface{}) (map[string]string, error) {
	values := c.Dump() // Use current values as defaults
	for name, value := range patch {
		values[name] = value
	}

	return c.update(values)
}

// HTTPSAddress is a convenience for loading the node configuration and
// returning the value of core.https_address.
func HTTPSAddress(node *db.Node) (string, error) {
	var config *Config
	err := node.Transaction(func(tx *db.NodeTx) error {
		var err error
		config, err = ConfigLoad(tx)
		return err
	})
	if err != nil {
		return "", err
	}

	return config.HTTPSAddress(), nil
}

// ClusterAddress is a convenience for loading the node configuration and
// returning the value of cluster.https_address.
func ClusterAddress(node *db.Node) (string, error) {
	var config *Config
	err := node.Transaction(func(tx *db.NodeTx) error {
		var err error
		config, err = ConfigLoad(tx)
		return err
	})
	if err != nil {
		return "", err
	}

	return config.ClusterAddress(), nil
}

// DebugAddress is a convenience for loading the node configuration and
// returning the value of core.debug_address.
func DebugAddress(node *db.Node) (string, error) {
	var config *Config
	err := node.Transaction(func(tx *db.NodeTx) error {
		var err error
		config, err = ConfigLoad(tx)
		return err
	})
	if err != nil {
		return "", err
	}

	return config.DebugAddress(), nil
}

func (c *Config) update(values map[string]interface{}) (map[string]string, error) {
	changed, err := c.m.Change(values)
	if err != nil {
		return nil, err
	}

	err = c.tx.UpdateConfig(changed)
	if err != nil {
		return nil, fmt.Errorf("cannot persist local configuration changes: %v", err)
	}

	return changed, nil
}

// ConfigSchema defines available server configuration keys.
var ConfigSchema = config.Schema{
	// Network address for this LXD server
	"core.https_address": {},

	// Network address for cluster communication
	"cluster.https_address": {},

	// Network address for the debug server
	"core.debug_address": {},

	// MAAS machine this LXD instance is associated with
	"maas.machine": {},
}
