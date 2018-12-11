package cluster

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"

	"golang.org/x/crypto/scrypt"

	"github.com/lxc/lxd/lxd/config"
	"github.com/lxc/lxd/lxd/db"
	"github.com/pkg/errors"
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

// TrustPassword returns the LXD trust password for authenticating clients.
func (c *Config) TrustPassword() string {
	return c.m.GetString("core.trust_password")
}

// CandidEndpoint returns the address of the Candid endpoint to use for
// authentication, if any.
func (c *Config) CandidEndpoint() string {
	return c.m.GetString("candid.api.url")
}

// CandidEndpointKey returns the public key for the API endpoint
func (c *Config) CandidEndpointKey() string {
	return c.m.GetString("candid.api.key")
}

// CandidExpiry returns the cookie expiry of the macaroon.
func (c *Config) CandidExpiry() int64 {
	return c.m.GetInt64("candid.expiry")
}

// CandidDomains returns the valid domains.
func (c *Config) CandidDomains() string {
	return c.m.GetString("candid.domains")
}

// AutoUpdateInterval returns the configured images auto update interval.
func (c *Config) AutoUpdateInterval() time.Duration {
	n := c.m.GetInt64("images.auto_update_interval")
	return time.Duration(n) * time.Hour
}

// RemoteCacheExpiry returns the configured expiration value for remote images
// expiration.
func (c *Config) RemoteCacheExpiry() int64 {
	return c.m.GetInt64("images.remote_cache_expiry")
}

// ProxyHTTPS returns the configured HTTPS proxy, if any.
func (c *Config) ProxyHTTPS() string {
	return c.m.GetString("core.proxy_https")
}

// ProxyHTTP returns the configured HTTP proxy, if any.
func (c *Config) ProxyHTTP() string {
	return c.m.GetString("core.proxy_http")
}

// ProxyIgnoreHosts returns the configured ignore-hosts proxy setting, if any.
func (c *Config) ProxyIgnoreHosts() string {
	return c.m.GetString("core.proxy_ignore_hosts")
}

// MAASController the configured MAAS url and key, if any.
func (c *Config) MAASController() (string, string) {
	url := c.m.GetString("maas.api.url")
	key := c.m.GetString("maas.api.key")
	return url, key
}

// OfflineThreshold returns the configured heartbeat threshold, i.e. the
// number of seconds before after which an unresponsive node is considered
// offline..
func (c *Config) OfflineThreshold() time.Duration {
	n := c.m.GetInt64("cluster.offline_threshold")
	return time.Duration(n) * time.Second
}

// ImagesMinimalReplica returns the numbers of nodes for cluster images replication
func (c *Config) ImagesMinimalReplica() int64 {
	return c.m.GetInt64("cluster.images_minimal_replica")
}

// Dump current configuration keys and their values. Keys with values matching
// their defaults are omitted.
func (c *Config) Dump() map[string]interface{} {
	return c.m.Dump()
}

// Replace the current configuration with the given values.
//
// Return what has actually changed.
func (c *Config) Replace(values map[string]interface{}) (map[string]string, error) {
	return c.update(values)
}

// Patch changes only the configuration keys in the given map.
//
// Return what has actually changed.
func (c *Config) Patch(patch map[string]interface{}) (map[string]string, error) {
	values := c.Dump() // Use current values as defaults
	for name, value := range patch {
		values[name] = value
	}
	return c.update(values)
}

func (c *Config) update(values map[string]interface{}) (map[string]string, error) {
	changed, err := c.m.Change(values)
	if err != nil {
		return nil, err
	}

	err = c.tx.UpdateConfig(changed)
	if err != nil {
		return nil, errors.Wrap(err, "cannot persist configuration changes: %v")
	}

	return changed, nil
}

// ConfigGetString is a convenience for loading the cluster configuration and
// returning the value of a particular key.
//
// It's a deprecated API meant to be used by call sites that are not
// interacting with the database in a transactional way.
func ConfigGetString(cluster *db.Cluster, key string) (string, error) {
	config, err := configGet(cluster)
	if err != nil {
		return "", err
	}
	return config.m.GetString(key), nil
}

// ConfigGetBool is a convenience for loading the cluster configuration and
// returning the value of a particular boolean key.
//
// It's a deprecated API meant to be used by call sites that are not
// interacting with the database in a transactional way.
func ConfigGetBool(cluster *db.Cluster, key string) (bool, error) {
	config, err := configGet(cluster)
	if err != nil {
		return false, err
	}
	return config.m.GetBool(key), nil
}

// ConfigGetInt64 is a convenience for loading the cluster configuration and
// returning the value of a particular key.
//
// It's a deprecated API meant to be used by call sites that are not
// interacting with the database in a transactional way.
func ConfigGetInt64(cluster *db.Cluster, key string) (int64, error) {
	config, err := configGet(cluster)
	if err != nil {
		return 0, err
	}
	return config.m.GetInt64(key), nil
}

func configGet(cluster *db.Cluster) (*Config, error) {
	var config *Config
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		config, err = ConfigLoad(tx)
		return err
	})
	return config, err
}

// ConfigSchema defines available server configuration keys.
var ConfigSchema = config.Schema{
	"backups.compression_algorithm":  {Default: "gzip", Validator: validateCompression},
	"cluster.offline_threshold":      {Type: config.Int64, Default: offlineThresholdDefault(), Validator: offlineThresholdValidator},
	"cluster.images_minimal_replica": {Type: config.Int64, Default: "3", Validator: imageMinimalReplicaValidator},
	"core.https_allowed_headers":     {},
	"core.https_allowed_methods":     {},
	"core.https_allowed_origin":      {},
	"core.https_allowed_credentials": {Type: config.Bool},
	"core.proxy_http":                {},
	"core.proxy_https":               {},
	"core.proxy_ignore_hosts":        {},
	"core.trust_password":            {Hidden: true, Setter: passwordSetter},
	"candid.api.key":                 {},
	"candid.api.url":                 {},
	"candid.domains":                 {},
	"candid.expiry":                  {Type: config.Int64, Default: "3600"},
	"images.auto_update_cached":      {Type: config.Bool, Default: "true"},
	"images.auto_update_interval":    {Type: config.Int64, Default: "6"},
	"images.compression_algorithm":   {Default: "gzip", Validator: validateCompression},
	"images.remote_cache_expiry":     {Type: config.Int64, Default: "10"},
	"maas.api.key":                   {},
	"maas.api.url":                   {},

	// Keys deprecated since the implementation of the storage api.
	"storage.lvm_fstype":           {Setter: deprecatedStorage, Default: "ext4"},
	"storage.lvm_mount_options":    {Setter: deprecatedStorage, Default: "discard"},
	"storage.lvm_thinpool_name":    {Setter: deprecatedStorage, Default: "LXDThinPool"},
	"storage.lvm_vg_name":          {Setter: deprecatedStorage},
	"storage.lvm_volume_size":      {Setter: deprecatedStorage, Default: "10GiB"},
	"storage.zfs_pool_name":        {Setter: deprecatedStorage},
	"storage.zfs_remove_snapshots": {Setter: deprecatedStorage, Type: config.Bool},
	"storage.zfs_use_refquota":     {Setter: deprecatedStorage, Type: config.Bool},
}

func offlineThresholdDefault() string {
	return strconv.Itoa(db.DefaultOfflineThreshold)
}

func offlineThresholdValidator(value string) error {
	// Ensure that the given value is greater than the heartbeat interval,
	// which is the lower bound granularity of the offline check.
	threshold, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("Offline threshold is not a number")
	}

	if threshold <= heartbeatInterval {
		return fmt.Errorf("Value must be greater than '%d'", heartbeatInterval)
	}

	return nil
}

func imageMinimalReplicaValidator(value string) error {
	count, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("Minimal image replica count is not a number")
	}

	if count < 1 && count != -1 {
		return fmt.Errorf("Invalid value for image replica count")
	}

	return nil
}

func passwordSetter(value string) (string, error) {
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

func validateCompression(value string) error {
	if value == "none" {
		return nil
	}

	_, err := exec.LookPath(value)
	return err
}

func deprecatedStorage(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return "", fmt.Errorf("deprecated: use storage pool configuration")
}
