package config

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/canonical/lxd/lxd/config"
	"github.com/canonical/lxd/lxd/db"
	scriptletLoad "github.com/canonical/lxd/lxd/scriptlet/load"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/validate"
)

// Config holds cluster-wide configuration values.
type Config struct {
	tx *db.ClusterTx // DB transaction the values in this config are bound to.
	m  config.Map    // Low-level map holding the config values.
}

// Load loads a new Config object with the current cluster configuration
// values fetched from the database.
func Load(ctx context.Context, tx *db.ClusterTx) (*Config, error) {
	// Load current raw values from the database, any error is fatal.
	values, err := tx.Config(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot fetch node config from database: %w", err)
	}

	m, err := config.SafeLoad(ConfigSchema, values)
	if err != nil {
		return nil, fmt.Errorf("failed to load node config: %w", err)
	}

	return &Config{tx: tx, m: m}, nil
}

// BackupsCompressionAlgorithm returns the compression algorithm to use for backups.
func (c *Config) BackupsCompressionAlgorithm() string {
	return c.m.GetString("backups.compression_algorithm")
}

// MetricsAuthentication checks whether metrics API requires authentication.
func (c *Config) MetricsAuthentication() bool {
	return c.m.GetBool("core.metrics_authentication")
}

// BGPASN returns the BGP ASN setting.
func (c *Config) BGPASN() int64 {
	return c.m.GetInt64("core.bgp_asn")
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

// TrustCACertificates returns whether client certificates are checked
// against a CA.
func (c *Config) TrustCACertificates() bool {
	return c.m.GetBool("core.trust_ca_certificates")
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

// HTTPSTrustedProxy returns the configured HTTPS trusted proxy setting, if any.
func (c *Config) HTTPSTrustedProxy() string {
	return c.m.GetString("core.https_trusted_proxy")
}

// MAASController the configured MAAS url and key, if any.
func (c *Config) MAASController() (apiURL string, apiKey string) {
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

// ImagesMinimalReplica returns the numbers of nodes for cluster images replication.
func (c *Config) ImagesMinimalReplica() int64 {
	return c.m.GetInt64("cluster.images_minimal_replica")
}

// MaxVoters returns the maximum number of members in a cluster that will be
// assigned the voter role.
func (c *Config) MaxVoters() int64 {
	return c.m.GetInt64("cluster.max_voters")
}

// MaxStandBy returns the maximum number of standby members in a cluster that
// will be assigned the stand-by role.
func (c *Config) MaxStandBy() int64 {
	return c.m.GetInt64("cluster.max_standby")
}

// NetworkOVNIntegrationBridge returns the integration OVS bridge to use for OVN networks.
func (c *Config) NetworkOVNIntegrationBridge() string {
	return c.m.GetString("network.ovn.integration_bridge")
}

// NetworkOVNNorthboundConnection returns the OVN northbound database connection string for OVN networks.
func (c *Config) NetworkOVNNorthboundConnection() string {
	return c.m.GetString("network.ovn.northbound_connection")
}

// NetworkOVNSSL returns all three SSL configuration keys needed for a connection.
func (c *Config) NetworkOVNSSL() (caCert string, clientCert string, clientKey string) {
	return c.m.GetString("network.ovn.ca_cert"), c.m.GetString("network.ovn.client_cert"), c.m.GetString("network.ovn.client_key")
}

// ShutdownTimeout returns the number of minutes to wait for running operation to complete
// before LXD server shut down.
func (c *Config) ShutdownTimeout() time.Duration {
	n := c.m.GetInt64("core.shutdown_timeout")
	return time.Duration(n) * time.Minute
}

// ImagesDefaultArchitecture returns the default architecture.
func (c *Config) ImagesDefaultArchitecture() string {
	return c.m.GetString("images.default_architecture")
}

// ImagesCompressionAlgorithm returns the compression algorithm to use for images.
func (c *Config) ImagesCompressionAlgorithm() string {
	return c.m.GetString("images.compression_algorithm")
}

// ImagesAutoUpdateCached returns whether or not to auto update cached images.
func (c *Config) ImagesAutoUpdateCached() bool {
	return c.m.GetBool("images.auto_update_cached")
}

// ImagesAutoUpdateIntervalHours returns interval in hours at which to look for update to cached images.
func (c *Config) ImagesAutoUpdateIntervalHours() int64 {
	return c.m.GetInt64("images.auto_update_interval")
}

// ImagesRemoteCacheExpiryDays returns the number of days after which an unused cached remote image will be flushed.
func (c *Config) ImagesRemoteCacheExpiryDays() int64 {
	return c.m.GetInt64("images.remote_cache_expiry")
}

// InstancesNICHostname returns hostname mode to use for instance NICs.
func (c *Config) InstancesNICHostname() string {
	return c.m.GetString("instances.nic.host_name")
}

// InstancesPlacementScriptlet returns the instances placement scriptlet source code.
func (c *Config) InstancesPlacementScriptlet() string {
	return c.m.GetString("instances.placement.scriptlet")
}

// InstancesMigrationStateful returns the whether or not to auto enable migration.stateful for all VM instances.
func (c *Config) InstancesMigrationStateful() bool {
	return c.m.GetBool("instances.migration.stateful")
}

// LokiServer returns all the Loki settings needed to connect to a server.
func (c *Config) LokiServer() (apiURL string, authUsername string, authPassword string, apiCACert string, instance string, logLevel string, labels []string, types []string) {
	if c.m.GetString("loki.types") != "" {
		types = strings.Split(c.m.GetString("loki.types"), ",")
	}

	if c.m.GetString("loki.labels") != "" {
		labels = strings.Split(c.m.GetString("loki.labels"), ",")
	}

	return c.m.GetString("loki.api.url"), c.m.GetString("loki.auth.username"), c.m.GetString("loki.auth.password"), c.m.GetString("loki.api.ca_cert"), c.m.GetString("loki.instance"), c.m.GetString("loki.loglevel"), labels, types
}

// ACME returns all ACME settings needed for certificate renewal.
func (c *Config) ACME() (domain string, email string, caURL string, agreeTOS bool) {
	return c.m.GetString("acme.domain"), c.m.GetString("acme.email"), c.m.GetString("acme.ca_url"), c.m.GetBool("acme.agree_tos")
}

// ClusterJoinTokenExpiry returns the cluster join token expiry.
func (c *Config) ClusterJoinTokenExpiry() string {
	return c.m.GetString("cluster.join_token_expiry")
}

// RemoteTokenExpiry returns the time after which a remote add token expires.
func (c *Config) RemoteTokenExpiry() string {
	return c.m.GetString("core.remote_token_expiry")
}

// OIDCServer returns all the OpenID Connect settings needed to connect to a server.
func (c *Config) OIDCServer() (issuer string, clientID string, audience string, groupsClaim string, scopes string) {
	return c.m.GetString("oidc.issuer"), c.m.GetString("oidc.client.id"), c.m.GetString("oidc.audience"), c.m.GetString("oidc.groups.claim"), c.m.GetString("oidc.scopes")
}

// ClusterHealingThreshold returns the configured healing threshold, i.e. the
// number of seconds after which an offline node will be evacuated automatically. If the config key
// is set but its value is lower than cluster.offline_threshold it returns
// the value of cluster.offline_threshold instead. If this feature is disabled, it returns 0.
func (c *Config) ClusterHealingThreshold() time.Duration {
	n := c.m.GetInt64("cluster.healing_threshold")
	if n == 0 {
		return 0
	}

	healingThreshold := time.Duration(n) * time.Second
	offlineThreshold := c.OfflineThreshold()

	if healingThreshold < offlineThreshold {
		return offlineThreshold
	}

	return healingThreshold
}

// Dump current configuration keys and their values. Keys with values matching
// their defaults are omitted.
func (c *Config) Dump() map[string]any {
	return c.m.Dump()
}

// Replace the current configuration with the given values.
//
// Return what has actually changed.
func (c *Config) Replace(values map[string]any) (map[string]string, error) {
	return c.update(values)
}

// Patch changes only the configuration keys in the given map.
//
// Return what has actually changed.
func (c *Config) Patch(patch map[string]any) (map[string]string, error) {
	values := c.Dump() // Use current values as defaults
	for name, value := range patch {
		values[name] = value
	}

	return c.update(values)
}

func (c *Config) update(values map[string]any) (map[string]string, error) {
	changed, err := c.m.Change(values)
	if err != nil {
		return nil, err
	}

	err = c.tx.UpdateClusterConfig(changed)
	if err != nil {
		return nil, fmt.Errorf("cannot persist configuration changes: %w", err)
	}

	return changed, nil
}

// ConfigSchema defines available server configuration keys.
var ConfigSchema = config.Schema{
	// lxdmeta:generate(entities=server; group=acme; key=acme.ca_url)
	//
	// ---
	//  type: string
	//  scope: global
	//  defaultdesc: `https://acme-v02.api.letsencrypt.org/directory`
	//  shortdesc: URL to the directory resource of the ACME service
	"acme.ca_url": {},

	// lxdmeta:generate(entities=server; group=acme; key=acme.domain)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Domain for which the certificate is issued
	"acme.domain": {},

	// lxdmeta:generate(entities=server; group=acme; key=acme.email)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Email address used for the account registration
	"acme.email": {},

	// lxdmeta:generate(entities=server; group=acme; key=acme.agree_tos)
	//
	// ---
	//  type: bool
	//  scope: global
	//  defaultdesc: `false`
	//  shortdesc: Agree to ACME terms of service
	"acme.agree_tos": {Type: config.Bool, Default: "false"},

	// lxdmeta:generate(entities=server; group=miscellaneous; key=backups.compression_algorithm)
	// Possible values are `bzip2`, `gzip`, `lzma`, `xz`, or `none`.
	// ---
	//  type: string
	//  scope: global
	//  defaultdesc: `gzip`
	//  shortdesc: Compression algorithm to use for backups
	"backups.compression_algorithm": {Default: "gzip", Validator: validate.IsCompressionAlgorithm},

	// lxdmeta:generate(entities=server; group=cluster; key=cluster.offline_threshold)
	// Specify the number of seconds after which an unresponsive member is considered offline.
	// ---
	//  type: integer
	//  scope: global
	//  defaultdesc: `20`
	//  shortdesc: Threshold when an unresponsive member is considered offline
	"cluster.offline_threshold": {Type: config.Int64, Default: offlineThresholdDefault(), Validator: offlineThresholdValidator},
	// lxdmeta:generate(entities=server; group=cluster; key=cluster.images_minimal_replica)
	// Specify the minimal number of cluster members that keep a copy of a particular image.
	// Set this option to `1` for no replication, or to `-1` to replicate images on all members.
	// ---
	//  type: integer
	//  scope: global
	//  defaultdesc: `3`
	//  shortdesc: Number of cluster members that replicate an image
	"cluster.images_minimal_replica": {Type: config.Int64, Default: "3", Validator: imageMinimalReplicaValidator},

	// lxdmeta:generate(entities=server; group=cluster; key=cluster.healing_threshold)
	// Specify the number of seconds after which an offline cluster member is to be evacuated.
	// To disable evacuating offline members, set this option to `0`.
	// ---
	//  type: integer
	//  scope: global
	//  defaultdesc: `0`
	//  shortdesc: Threshold when to evacuate an offline cluster member
	"cluster.healing_threshold": {Type: config.Int64, Default: "0"},

	// lxdmeta:generate(entities=server; group=cluster; key=cluster.join_token_expiry)
	//
	// ---
	//  type: string
	//  scope: global
	//  defaultdesc: `3H`
	//  shortdesc: Time after which a cluster join token expires
	"cluster.join_token_expiry": {Type: config.String, Default: "3H", Validator: expiryValidator},

	// lxdmeta:generate(entities=server; group=cluster; key=cluster.max_voters)
	// Specify the maximum number of cluster members that are assigned the database voter role.
	// This must be an odd number >= `3`.
	// ---
	//  type: integer
	//  scope: global
	//  defaultdesc: `3`
	//  shortdesc: Number of database voter members
	"cluster.max_voters": {Type: config.Int64, Default: "3", Validator: maxVotersValidator},

	// lxdmeta:generate(entities=server; group=cluster; key=cluster.max_standby)
	// Specify the maximum number of cluster members that are assigned the database stand-by role.
	// This must be a number between `0` and `5`.
	// ---
	//  type: integer
	//  scope: global
	//  defaultdesc: `2`
	//  shortdesc: Number of database stand-by members
	"cluster.max_standby": {Type: config.Int64, Default: "2", Validator: maxStandByValidator},

	// lxdmeta:generate(entities=server; group=core; key=core.metrics_authentication)
	//
	// ---
	//  type: bool
	//  scope: global
	//  defaultdesc: `true`
	//  shortdesc: Whether to enforce authentication on the metrics endpoint
	"core.metrics_authentication": {Type: config.Bool, Default: "true"},

	// lxdmeta:generate(entities=server; group=core; key=core.bgp_asn)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: BGP Autonomous System Number for the local server
	"core.bgp_asn": {Type: config.Int64, Default: "0", Validator: validate.Optional(validate.IsInRange(0, 4294967294))},

	// lxdmeta:generate(entities=server; group=core; key=core.https_allowed_headers)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: `Access-Control-Allow-Headers` HTTP header value
	"core.https_allowed_headers": {},

	// lxdmeta:generate(entities=server; group=core; key=core.https_allowed_methods)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: `Access-Control-Allow-Methods` HTTP header value
	"core.https_allowed_methods": {},

	// lxdmeta:generate(entities=server; group=core; key=core.https_allowed_origin)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: `Access-Control-Allow-Origin` HTTP header value
	"core.https_allowed_origin": {},

	// lxdmeta:generate(entities=server; group=core; key=core.https_allowed_credentials)
	// If enabled, the `Access-Control-Allow-Credentials` HTTP header value is set to `true`.
	// ---
	//  type: bool
	//  scope: global
	//  defaultdesc: `false`
	//  shortdesc: Whether to set `Access-Control-Allow-Credentials`
	"core.https_allowed_credentials": {Type: config.Bool, Default: "false"},

	// lxdmeta:generate(entities=server; group=core; key=core.https_trusted_proxy)
	// Specify a comma-separated list of IP addresses of trusted servers that provide the client's address through the proxy connection header.
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Trusted servers to provide the client's address
	"core.https_trusted_proxy": {},

	// lxdmeta:generate(entities=server; group=core; key=core.proxy_http)
	// If this option is not specified, LXD falls back to the `HTTP_PROXY` environment variable (if set).
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: HTTP proxy to use
	"core.proxy_http": {},

	// lxdmeta:generate(entities=server; group=core; key=core.proxy_https)
	// If this option is not specified, LXD falls back to the `HTTPS_PROXY` environment variable (if set).
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: HTTPS proxy to use
	"core.proxy_https": {},

	// lxdmeta:generate(entities=server; group=core; key=core.proxy_ignore_hosts)
	// Specify this option in a similar format to `NO_PROXY` (for example, `1.2.3.4,1.2.3.5`)
	//
	// If this option is not specified, LXD falls back to the `NO_PROXY` environment variable (if set).
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Hosts that don't need the proxy
	"core.proxy_ignore_hosts": {},

	// lxdmeta:generate(entities=server; group=core; key=core.remote_token_expiry)
	//
	// ---
	//  type: string
	//  scope: global
	//  defaultdesc: no expiry
	//  shortdesc: Time after which a remote add token expires
	"core.remote_token_expiry": {Type: config.String, Validator: validate.Optional(expiryValidator)},

	// lxdmeta:generate(entities=server; group=core; key=core.shutdown_timeout)
	// Specify the number of minutes to wait for running operations to complete before the LXD server shuts down.
	// ---
	//  type: integer
	//  scope: global
	//  defaultdesc: `5`
	//  shortdesc: How long to wait before shutdown
	"core.shutdown_timeout": {Type: config.Int64, Default: "5"},

	// lxdmeta:generate(entities=server; group=core; key=core.trust_ca_certificates)
	//
	// ---
	//  type: bool
	//  scope: global
	//  defaultdesc: `false`
	//  shortdesc: Whether to automatically trust clients signed by the CA
	"core.trust_ca_certificates": {Type: config.Bool, Default: "false"},

	// lxdmeta:generate(entities=server; group=images; key=images.auto_update_cached)
	//
	// ---
	//  type: bool
	//  scope: global
	//  defaultdesc: `true`
	//  shortdesc: Whether to automatically update cached images
	"images.auto_update_cached": {Type: config.Bool, Default: "true"},

	// lxdmeta:generate(entities=server; group=images; key=images.auto_update_interval)
	// Specify the interval in hours.
	// To disable looking for updates to cached images, set this option to `0`.
	// ---
	//  type: integer
	//  scope: global
	//  defaultdesc: `6`
	//  shortdesc: Interval at which to look for updates to cached images
	"images.auto_update_interval": {Type: config.Int64, Default: "6"},

	// lxdmeta:generate(entities=server; group=images; key=images.compression_algorithm)
	// Possible values are `bzip2`, `gzip`, `lzma`, `xz`, or `none`.
	// ---
	//  type: string
	//  scope: global
	//  defaultdesc: `gzip`
	//  shortdesc: Compression algorithm to use for new images
	"images.compression_algorithm": {Default: "gzip", Validator: validate.IsCompressionAlgorithm},

	// lxdmeta:generate(entities=server; group=images; key=images.default_architecture)
	//
	// ---
	//  type: string
	//  shortdesc: Default architecture to use in a mixed-architecture cluster
	"images.default_architecture": {Validator: validate.Optional(validate.IsArchitecture)},

	// lxdmeta:generate(entities=server; group=images; key=images.remote_cache_expiry)
	// Specify the number of days after which the unused cached image expires.
	// ---
	//  type: integer
	//  scope: global
	//  defaultdesc: `10`
	//  shortdesc: When an unused cached remote image is flushed
	"images.remote_cache_expiry": {Type: config.Int64, Default: "10"},

	// lxdmeta:generate(entities=server; group=miscellaneous; key=instances.nic.host_name)
	// Possible values are `random` and `mac`.
	//
	// If set to `random`, use the random host interface name as the host name.
	// If set to `mac`, generate a host name in the form `lxd<mac_address>` (MAC without leading two digits).
	// ---
	//  type: string
	//  scope: global
	//  defaultdesc: `random`
	//  shortdesc: How to set the host name for a NIC
	"instances.nic.host_name": {Validator: validate.Optional(validate.IsOneOf("random", "mac"))},

	// lxdmeta:generate(entities=server; group=miscellaneous; key=instances.placement.scriptlet)
	// When using custom automatic instance placement logic, this option stores the scriptlet.
	// See {ref}`clustering-instance-placement-scriptlet` for more information.
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Instance placement scriptlet for automatic instance placement
	"instances.placement.scriptlet": {Validator: validate.Optional(scriptletLoad.InstancePlacementValidate)},

	// lxdmeta:generate(entities=server; group=miscellaneous; key=instances.migration.stateful)
	// You can override this setting for relevant instances, either in the instance-specific configuration or through a profile.
	// ---
	//  type: bool
	//  scope: global
	//  shortdesc: Whether to set `migration.stateful` to `true` for the instances
	"instances.migration.stateful": {Type: config.Bool, Default: "false"},

	// lxdmeta:generate(entities=server; group=loki; key=loki.auth.username)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: User name used for Loki authentication
	"loki.auth.username": {},

	// lxdmeta:generate(entities=server; group=loki; key=loki.auth.password)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Password used for Loki authentication
	"loki.auth.password": {},

	// lxdmeta:generate(entities=server; group=loki; key=loki.api.ca_cert)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: CA certificate for the Loki server
	"loki.api.ca_cert": {},

	// lxdmeta:generate(entities=server; group=loki; key=loki.api.url)
	// Specify the protocol, name or IP and port. For example `https://loki.example.com:3100`. LXD will automatically add the `/loki/api/v1/push` suffix so there's no need to add it here.
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: URL to the Loki server
	"loki.api.url": {},

	// lxdmeta:generate(entities=server; group=loki; key=loki.instance)
	// This allows replacing the default instance value (server host name) by a more relevant value like a cluster identifier.
	// ---
	//  type: string
	//  scope: global
	//  defaultdesc: Local server host name or cluster member name
	//  shortdesc: Name to use as the instance field in Loki events.
	"loki.instance": {},

	// lxdmeta:generate(entities=server; group=loki; key=loki.labels)
	// Specify a comma-separated list of values that should be used as labels for a Loki log entry.
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Labels for a Loki log entry
	"loki.labels": {},
	// lxdmeta:generate(entities=server; group=loki; key=loki.loglevel)
	//
	// ---
	//  type: string
	//  scope: global
	//  defaultdesc: `info`
	//  shortdesc: Minimum log level to send to the Loki server
	"loki.loglevel": {Validator: logLevelValidator, Default: logrus.InfoLevel.String()},

	// lxdmeta:generate(entities=server; group=loki; key=loki.types)
	// Specify a comma-separated list of events to send to the Loki server.
	// The events can be any combination of `lifecycle`, `logging`, and `ovn`.
	// ---
	//  type: string
	//  scope: global
	//  defaultdesc: `lifecycle,logging`
	//  shortdesc: Events to send to the Loki server
	"loki.types": {Validator: validate.Optional(validate.IsListOf(validate.IsOneOf("lifecycle", "logging", "ovn"))), Default: "lifecycle,logging"},

	// lxdmeta:generate(entities=server; group=miscellaneous; key=maas.api.key)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: API key to manage MAAS
	"maas.api.key": {},

	// lxdmeta:generate(entities=server; group=miscellaneous; key=maas.api.url)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: URL of the MAAS server
	"maas.api.url": {},

	// lxdmeta:generate(entities=server; group=oidc; key=oidc.client.id)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: OpenID Connect client ID
	"oidc.client.id": {},

	// lxdmeta:generate(entities=server; group=oidc; key=oidc.issuer)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: OpenID Connect Discovery URL for the provider
	"oidc.issuer": {},

	// lxdmeta:generate(entities=server; group=oidc; key=oidc.audience)
	// This value is required by some providers.
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Expected audience value for the application
	"oidc.audience": {},

	// lxdmeta:generate(entities=server; group=oidc; key=oidc.groups.claim)
	// Specify a custom claim to be requested when performing OIDC flows.
	// Configure a corresponding custom claim in your identity provider and
	// add organization level groups to it. These can be mapped to LXD groups
	// for automatic access control.
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: A claim used for mapping identity provider groups to LXD groups.
	"oidc.groups.claim": {},

	// lxdmeta:generate(entities=server; group=oidc; key=oidc.scopes)
	// Overwrite the default scopes that are requested during OIDC flows.
	// If not set, the flow will request oidc, offline_access, email,
	// profile, and if defined the oidc_groups_claim as scopes.
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Scopes requested during OIDC flows
	"oidc.scopes": {},
	// OVN networking global keys.

	// lxdmeta:generate(entities=server; group=miscellaneous; key=network.ovn.integration_bridge)
	//
	// ---
	//  type: string
	//  scope: global
	//  defaultdesc: `br-int`
	//  shortdesc: OVS integration bridge to use for OVN networks
	"network.ovn.integration_bridge": {Default: "br-int"},
	// lxdmeta:generate(entities=server; group=miscellaneous; key=network.ovn.northbound_connection)
	//
	// ---
	//  type: string
	//  scope: global
	//  defaultdesc: `unix:/var/run/ovn/ovnnb_db.sock`
	//  shortdesc: OVN northbound database connection string
	"network.ovn.northbound_connection": {Default: "unix:/var/run/ovn/ovnnb_db.sock"},

	// lxdmeta:generate(entities=server; group=miscellaneous; key=network.ovn.ca_cert)
	//
	// ---
	//  type: string
	//  scope: global
	//  defaultdesc: Content of `/etc/ovn/ovn-central.crt` if present
	//  shortdesc: OVN SSL certificate authority
	"network.ovn.ca_cert": {Default: ""},

	// lxdmeta:generate(entities=server; group=miscellaneous; key=network.ovn.client_cert)
	//
	// ---
	//  type: string
	//  scope: global
	//  defaultdesc: Content of `/etc/ovn/cert_host` if present
	//  shortdesc: OVN SSL client certificate
	"network.ovn.client_cert": {Default: ""},

	// lxdmeta:generate(entities=server; group=miscellaneous; key=network.ovn.client_key)
	//
	// ---
	//  type: string
	//  scope: global
	//  defaultdesc: Content of `/etc/ovn/key_host` if present
	//  shortdesc: OVN SSL client key
	"network.ovn.client_key": {Default: ""},
}

func expiryValidator(value string) error {
	_, err := shared.GetExpiry(time.Time{}, value)
	if err != nil {
		return err
	}

	return nil
}

func logLevelValidator(value string) error {
	if value == "" {
		return nil
	}

	_, err := logrus.ParseLevel(value)
	if err != nil {
		return err
	}

	return nil
}

func offlineThresholdDefault() string {
	return strconv.Itoa(db.DefaultOfflineThreshold)
}

func offlineThresholdValidator(value string) error {
	minThreshold := 10

	// Ensure that the given value is greater than the heartbeat interval,
	// which is the lower bound granularity of the offline check.
	threshold, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("Offline threshold is not a number")
	}

	if threshold <= minThreshold {
		return fmt.Errorf("Value must be greater than %d", minThreshold)
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

func maxVotersValidator(value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("Value is not a number")
	}

	if n < 3 || n%2 != 1 {
		return fmt.Errorf("Value must be an odd number equal to or higher than 3")
	}

	return nil
}

func maxStandByValidator(value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("Value is not a number")
	}

	if n < 0 || n > 5 {
		return fmt.Errorf("Value must be between 0 and 5")
	}

	return nil
}
