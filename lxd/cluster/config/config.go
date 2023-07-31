package config

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/scrypt"

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

// TrustPassword returns the LXD trust password for authenticating clients.
func (c *Config) TrustPassword() string {
	return c.m.GetString("core.trust_password")
}

// TrustCACertificates returns whether client certificates are checked
// against a CA.
func (c *Config) TrustCACertificates() bool {
	return c.m.GetBool("core.trust_ca_certificates")
}

// CandidServer returns all the Candid settings needed to connect to a server.
func (c *Config) CandidServer() (string, string, int64, string) {
	return c.m.GetString("candid.api.url"),
		c.m.GetString("candid.api.key"),
		c.m.GetInt64("candid.expiry"),
		c.m.GetString("candid.domains")
}

// RBACServer returns all the Candid settings needed to connect to a server.
func (c *Config) RBACServer() (string, string, int64, string, string, string, string) {
	return c.m.GetString("rbac.api.url"),
		c.m.GetString("rbac.api.key"),
		c.m.GetInt64("rbac.expiry"),
		c.m.GetString("rbac.agent.url"),
		c.m.GetString("rbac.agent.username"),
		c.m.GetString("rbac.agent.private_key"),
		c.m.GetString("rbac.agent.public_key")
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

// LokiServer returns all the Loki settings needed to connect to a server.
func (c *Config) LokiServer() (string, string, string, string, []string, string, []string) {
	var types []string
	var labels []string

	if c.m.GetString("loki.types") != "" {
		types = strings.Split(c.m.GetString("loki.types"), ",")
	}

	if c.m.GetString("loki.labels") != "" {
		labels = strings.Split(c.m.GetString("loki.labels"), ",")
	}

	return c.m.GetString("loki.api.url"), c.m.GetString("loki.auth.username"), c.m.GetString("loki.auth.password"), c.m.GetString("loki.api.ca_cert"), labels, c.m.GetString("loki.loglevel"), types
}

// ACME returns all ACME settings needed for certificate renewal.
func (c *Config) ACME() (string, string, string, bool) {
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
func (c *Config) OIDCServer() (string, string, string) {
	return c.m.GetString("oidc.issuer"), c.m.GetString("oidc.client.id"), c.m.GetString("oidc.audience")
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
	// lxddoc:generate(group=server-acme, key=acme.ca_url)
	//
	// ---
	//  type: string
	//  scope: global
	//  default: `https://acme-v02.api.letsencrypt.org/directory`
	//  shortdesc: Agree to ACME terms of service
	"acme.ca_url": {},
	// lxddoc:generate(group=server-acme, key=acme.domain)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Domain for which the certificate is issued
	"acme.domain": {},
	// lxddoc:generate(group=server-acme, key=acme.email)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Email address used for the account registration
	"acme.email": {},
	// lxddoc:generate(group=server-acme, key=acme.agree_tos)
	//
	// ---
	//  type: bool
	//  scope: global
	//  default: `false`
	//  shortdesc: Agree to ACME terms of service
	"acme.agree_tos":                 {Type: config.Bool, Default: "false"},
	"backups.compression_algorithm":  {Default: "gzip", Validator: validate.IsCompressionAlgorithm},
	"cluster.offline_threshold":      {Type: config.Int64, Default: offlineThresholdDefault(), Validator: offlineThresholdValidator},
	"cluster.images_minimal_replica": {Type: config.Int64, Default: "3", Validator: imageMinimalReplicaValidator},
	"cluster.healing_threshold":      {Type: config.Int64, Default: "0"},
	"cluster.join_token_expiry":      {Type: config.String, Default: "3H", Validator: expiryValidator},
	"cluster.max_voters":             {Type: config.Int64, Default: "3", Validator: maxVotersValidator},
	"cluster.max_standby":            {Type: config.Int64, Default: "2", Validator: maxStandByValidator},
	// lxddoc:generate(group=server-core, key=core.metrics_authentication)
	//
	// ---
	//  type: bool
	//  scope: global
	//  default: `true`
	//  shortdesc: Whether to enforce authentication on the metrics endpoint
	"core.metrics_authentication": {Type: config.Bool, Default: "true"},
	// lxddoc:generate(group=server-core, key=core.bgp_asn)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: The BGP Autonomous System Number to use for the local server
	"core.bgp_asn": {Type: config.Int64, Default: "0", Validator: validate.Optional(validate.IsInRange(0, 4294967294))},
	// lxddoc:generate(group=server-core, key=core.https_allowed_headers)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: `Access-Control-Allow-Headers` HTTP header value
	"core.https_allowed_headers": {},
	// lxddoc:generate(group=server-core, key=core.https_allowed_methods)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: `Access-Control-Allow-Methods` HTTP header value
	"core.https_allowed_methods": {},
	// lxddoc:generate(group=server-core, key=core.https_allowed_origin)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: `Access-Control-Allow-Origin` HTTP header value
	"core.https_allowed_origin": {},
	// lxddoc:generate(group=server-core, key=core.https_allowed_credentials)
	//
	// ---
	//  type: bool
	//  scope: global
	//  shortdesc: Whether to set the `Access-Control-Allow-Credentials` HTTP header value to `true`
	"core.https_allowed_credentials": {Type: config.Bool},
	// lxddoc:generate(group=server-core, key=core.https_trusted_proxy)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Comma-separated list of IP addresses of trusted servers to provide the client’s address through the proxy connection header
	"core.https_trusted_proxy": {},
	// lxddoc:generate(group=server-core, key=core.proxy_http)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: HTTP proxy to use, if any (falls back to `HTTP_PROXY` environment variable)
	"core.proxy_http": {},
	// lxddoc:generate(group=server-core, key=core.proxy_https)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: HTTPS proxy to use, if any (falls back to `HTTPS_PROXY` environment variable)
	"core.proxy_https": {},
	// lxddoc:generate(group=server-core, key=core.proxy_ignore_hosts)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Hosts that don’t need the proxy (similar format to `NO_PROXY`, for example, `1.2.3.4,1.2.3.5`, falls back to `NO_PROXY` environment variable)
	"core.proxy_ignore_hosts": {},
	// lxddoc:generate(group=server-core, key=core.remote_token_expiry)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Time after which a remote add token expires (defaults to no expiry)
	"core.remote_token_expiry": {Type: config.String, Validator: validate.Optional(expiryValidator)},
	// lxddoc:generate(group=server-core, key=core.shutdown_timeout)
	//
	// ---
	//  type: integer
	//  scope: global
	//  default: `5`
	//  shortdesc: Number of minutes to wait for running operations to complete before the LXD server shuts down
	"core.shutdown_timeout": {Type: config.Int64, Default: "5"},
	// lxddoc:generate(group=server-core, key=core.trust_password)
	//
	// ---
	//  type: string
	//  scope: global
	//  shortdesc: Password to be provided by clients to set up a trust
	"core.trust_password": {Hidden: true, Setter: passwordSetter},
	// lxddoc:generate(group=server-core, key=core.trust_ca_certificates)
	//
	// ---
	//  type: bool
	//  scope: global
	//  shortdesc: Whether to automatically trust clients signed by the CA
	"core.trust_ca_certificates":    {Type: config.Bool},
	"candid.api.key":                {},
	"candid.api.url":                {},
	"candid.domains":                {},
	"candid.expiry":                 {Type: config.Int64, Default: "3600"},
	"images.auto_update_cached":     {Type: config.Bool, Default: "true"},
	"images.auto_update_interval":   {Type: config.Int64, Default: "6"},
	"images.compression_algorithm":  {Default: "gzip", Validator: validate.IsCompressionAlgorithm},
	"images.default_architecture":   {Validator: validate.Optional(validate.IsArchitecture)},
	"images.remote_cache_expiry":    {Type: config.Int64, Default: "10"},
	"instances.nic.host_name":       {Validator: validate.Optional(validate.IsOneOf("random", "mac"))},
	"instances.placement.scriptlet": {Validator: validate.Optional(scriptletLoad.InstancePlacementValidate)},
	"loki.auth.username":            {},
	"loki.auth.password":            {Hidden: true},
	"loki.api.ca_cert":              {},
	"loki.api.url":                  {},
	"loki.labels":                   {},
	"loki.loglevel":                 {Validator: logLevelValidator, Default: logrus.InfoLevel.String()},
	"loki.types":                    {Validator: validate.Optional(validate.IsListOf(validate.IsOneOf("lifecycle", "logging"))), Default: "lifecycle,logging"},
	"maas.api.key":                  {},
	"maas.api.url":                  {},
	"oidc.client.id":                {},
	"oidc.issuer":                   {},
	"oidc.audience":                 {},
	"rbac.agent.url":                {},
	"rbac.agent.username":           {},
	"rbac.agent.private_key":        {},
	"rbac.agent.public_key":         {},
	"rbac.api.expiry":               {Type: config.Int64, Default: "3600"},
	"rbac.api.key":                  {},
	"rbac.api.url":                  {},
	"rbac.expiry":                   {Type: config.Int64, Default: "3600"},

	// OVN networking global keys.
	"network.ovn.integration_bridge":    {Default: "br-int"},
	"network.ovn.northbound_connection": {Default: "unix:/var/run/ovn/ovnnb_db.sock"},
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
		return fmt.Errorf("Value must be greater than '%d'", minThreshold)
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
