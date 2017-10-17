package cluster

import (
	"fmt"

	"github.com/lxc/lxd/lxd/config"
	"github.com/lxc/lxd/lxd/db"
)

// Config holds cluster-wide configuration values.
type Config struct {
	tx *db.ClusterTx // DB transaction the values in this config are bound to.
	m  config.Map    // Low-level map holding the config values.
}

// ConfigLoad loads a new Config object with the current cluster configuration
// values fetched from the database.
func ConfigLoad(tx *db.ClusterTx) (*Config, error) {
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

// HTTPSAllowedHeaders returns the relevant CORS setting.
func (c *Config) HTTPSAllowedHeaders() string {
	return c.m.GetString("core.https_allowed_headers")
}

// HTTPSAllowedMethods returns the relevant CORS setting.
func (c *Config) HTTPSAllowedMethods() string {
	return c.m.GetString("core.https_allowed_methods")
}

// HTTPSAllowedOrigin returns the relevant CORS setting.
func (c *Config) HTTPSAllowedOrigin() string {
	return c.m.GetString("core.https_allowed_origin")
}

// HTTPSAllowedCredentials returns the relevant CORS setting.
func (c *Config) HTTPSAllowedCredentials() bool {
	return c.m.GetBool("core.https_allowed_credentials")
}

// ProxyHTTP returns the configured HTTP proxy, if any.
func (c *Config) ProxyHTTP() string {
	return c.m.GetString("core.proxy_http")
}

// Dump current configuration keys and their values. Keys with values matching
// their defaults are omitted.
func (c *Config) Dump() map[string]interface{} {
	return c.m.Dump()
}

// Replace the current configuration with the given values.
func (c *Config) Replace(values map[string]interface{}) error {
	return c.update(values)
}

// Patch changes only the configuration keys in the given map.
func (c *Config) Patch(patch map[string]interface{}) error {
	values := c.Dump() // Use current values as defaults
	for name, value := range patch {
		values[name] = value
	}
	return c.update(values)
}

func (c *Config) update(values map[string]interface{}) error {
	changed, err := c.m.Change(values)
	if err != nil {
		return fmt.Errorf("invalid configuration changes: %s", err)
	}

	err = c.tx.UpdateConfig(changed)
	if err != nil {
		return fmt.Errorf("cannot persist confiuration changes: %v", err)
	}

	return nil
}

// ConfigSchema defines available server configuration keys.
var ConfigSchema = config.Schema{
	"core.https_allowed_headers":     {},
	"core.https_allowed_methods":     {},
	"core.https_allowed_origin":      {},
	"core.https_allowed_credentials": {Type: config.Bool},
	"core.proxy_http":                {},
	"core.proxy_https":               {},
	"core.proxy_ignore_hosts":        {},
	"core.trust_password":            {Hidden: true},
	"core.macaroon.endpoint":         {},
	"images.auto_update_cached":      {Type: config.Bool, Default: "true"},
	"images.auto_update_interval":    {Type: config.Int64, Default: "6"},
	"images.compression_algorithm":   {Default: "gzip"},
	"images.remote_cache_expiry":     {Type: config.Int64, Default: "10"},
	"maas.api.key":                   {},
	"maas.api.url":                   {},
	"maas.machine":                   {},

	// Keys deprecated since the implementation of the storage api.
	"storage.lvm_fstype":           {Setter: deprecatedStorage, Default: "ext4"},
	"storage.lvm_mount_options":    {Setter: deprecatedStorage, Default: "discard"},
	"storage.lvm_thinpool_name":    {Setter: deprecatedStorage, Default: "LXDPool"},
	"storage.lvm_vg_name":          {Setter: deprecatedStorage},
	"storage.lvm_volume_size":      {Setter: deprecatedStorage, Default: "10GiB"},
	"storage.zfs_pool_name":        {Setter: deprecatedStorage},
	"storage.zfs_remove_snapshots": {Setter: deprecatedStorage, Type: config.Bool},
	"storage.zfs_use_refquota":     {Setter: deprecatedStorage, Type: config.Bool},
}

func deprecatedStorage(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return "", fmt.Errorf("deprecated: use storage pool configuration")
}
