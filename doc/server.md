(server)=
# Server configuration

The server configuration is a simple set of key and values.

The key/value configuration is namespaced with the following namespaces
currently supported:

- `backups` (backups configuration)
- `candid` (External user authentication through Candid)
- `cluster` (cluster configuration)
- `core` (core daemon configuration)
- `images` (image configuration)
- `instances` (instance configuration)
- `maas` (MAAS integration)
- `rbac` (Role Based Access Control through external Candid + Canonical RBAC)
- `loki` (External log aggregation system)

```{rst-class} break-col-4 min-width-4-8
```

Key                                 | Type      | Scope     | Default                           | Description
:--                                 | :---      | :----     | :------                           | :----------
`backups.compression_algorithm`     | string    | global    | `gzip`                            | Compression algorithm to use for new images (`bzip2`, `gzip`, `lzma`, `xz` or `none`)
`candid.api.key`                    | string    | global    | -                                 | Public key of the candid server (required for HTTP-only servers)
`candid.api.url`                    | string    | global    | -                                 | URL of the the external authentication endpoint using Candid
`candid.domains`                    | string    | global    | -                                 | Comma-separated list of allowed Candid domains (empty string means all domains are valid)
`candid.expiry`                     | integer   | global    | `3600`                            | Candid macaroon expiry in seconds
`cluster.https_address`             | string    | local     | -                                 | Address to use for clustering traffic
`cluster.images_minimal_replica`    | integer   | global    | `3`                               | Minimal numbers of cluster members with a copy of a particular image (set 1 for no replication, -1 for all members)
`cluster.max_standby`               | integer   | global    | `2`                               | Maximum number of cluster members that will be assigned the database stand-by role
`cluster.max_voters`                | integer   | global    | `3`                               | Maximum number of cluster members that will be assigned the database voter role
`cluster.offline_threshold`         | integer   | global    | `20`                              | Number of seconds after which an unresponsive node is considered offline
`core.bgp_address`                  | string    | local     | -                                 | Address to bind the BGP server to (BGP)
`core.bgp_asn`                      | string    | global    | -                                 | The BGP Autonomous System Number to use for the local server
`core.bgp_routerid`                 | string    | local     | -                                 | A unique identifier for this BGP server (formatted as an IPv4 address)
`core.debug_address`                | string    | local     | -                                 | Address to bind the pprof debug server to (HTTP)
`core.dns_address`                  | string    | local     | -                                 | Address to bind the authoritative DNS server to (DNS)
`core.https_address`                | string    | local     | -                                 | Address to bind for the remote API (HTTPS)
`core.https_allowed_credentials`    | bool      | global    | -                                 | Whether to set Access-Control-Allow-Credentials HTTP header value to `true`
`core.https_allowed_headers`        | string    | global    | -                                 | Access-Control-Allow-Headers HTTP header value
`core.https_allowed_methods`        | string    | global    | -                                 | Access-Control-Allow-Methods HTTP header value
`core.https_allowed_origin`         | string    | global    | -                                 | Access-Control-Allow-Origin HTTP header value
`core.https_trusted_proxy`          | string    | global    | -                                 | Comma-separated list of IP addresses of trusted servers to provide the client's address through the proxy connection header
`core.metrics_address`              | string    | global    | -                                 | Address to bind the metrics server to (HTTPS)
`core.metrics_authentication`       | bool      | global    | `true`                            | Whether to enforce authentication on the metrics endpoint
`core.proxy_https`                  | string    | global    | -                                 | HTTPS proxy to use, if any (falls back to `HTTPS_PROXY` environment variable)
`core.proxy_http`                   | string    | global    | -                                 | HTTP proxy to use, if any (falls back to `HTTP_PROXY` environment variable)
`core.proxy_ignore_hosts`           | string    | global    | -                                 | Hosts which don't need the proxy for use (similar format to `NO_PROXY`, e.g. 1.2.3.4,1.2.3.5, falls back to `NO_PROXY` environment variable)
`core.shutdown_timeout`             | integer   | global    | `5`                               | Number of minutes to wait for running operations to complete before LXD server shut down
`core.storage_buckets_address`      | string    | local     | -                                 | Address to bind the storage object server to (HTTPS)
`core.trust_ca_certificates`        | bool      | global    | -                                 | Whether to automatically trust clients signed by the CA
`core.trust_password`               | string    | global    | -                                 | Password to be provided by clients to set up a trust
`images.auto_update_cached`         | bool      | global    | `true`                            | Whether to automatically update any image that LXD caches
`images.auto_update_interval`       | integer   | global    | `6`                               | Interval in hours at which to look for update to cached images (0 disables it)
`images.compression_algorithm`      | string    | global    | `gzip`                            | Compression algorithm to use for new images (`bzip2`, `gzip`, `lzma`, `xz` or `none`)
`images.default_architecture`       | string    | -         | -                                 | Default architecture which should be used in mixed architecture cluster
`images.remote_cache_expiry`        | integer   | global    | `10`                              | Number of days after which an unused cached remote image will be flushed
`instances.nic.host_name`           | string    | global    | `random`                          | If it is set to `random` then use the random host interface names but if it's set to mac, then generate a name in the form `lxd<mac_address>`(MAC without leading 2 digits).
`loki.api.ca_cert`                  | string    | global    | -                                 | The CA certificate for the Loki server
`loki.api.url`                      | string    | global    | -                                 | The URL to the Loki server
`loki.auth.password`                | string    | global    | -                                 | The password used for authentication
`loki.auth.username`                | string    | global    | -                                 | The username used for authentication
`loki.labels`                       | string    | global    | -                                 | Comma-separated list of values which should be used as labels for a Loki log entry
`loki.loglevel`                     | string    | global    | -                                 | Minimum log level to send to Loki server
`loki.types`                        | string    | global    | -                                 | Type of events to send to Loki server (`lifecycle` and/or `logging`)
`maas.api.key`                      | string    | global    | -                                 | API key to manage MAAS
`maas.api.url`                      | string    | global    | -                                 | URL of the MAAS server
`maas.machine`                      | string    | local     | host name                         | Name of this LXD host in MAAS
`network.ovn.integration_bridge`    | string    | global    | `br-int`                          | OVS integration bridge to use for OVN networks
`network.ovn.northbound_connection` | string    | global    | `unix:/var/run/ovn/ovnnb_db.sock` | OVN northbound database connection string
`rbac.agent.private_key`            | string    | global    | -                                 | The Candid agent private key as provided during RBAC registration
`rbac.agent.public_key`             | string    | global    | -                                 | The Candid agent public key as provided during RBAC registration
`rbac.agent.url`                    | string    | global    | -                                 | The Candid agent URL as provided during RBAC registration
`rbac.agent.username`               | string    | global    | -                                 | The Candid agent username as provided during RBAC registration
`rbac.api.expiry`                   | integer   | global    | -                                 | RBAC macaroon expiry in seconds
`rbac.api.key`                      | string    | global    | -                                 | Public key of the RBAC server (required for HTTP-only servers)
`rbac.api.url`                      | string    | global    | -                                 | URL of the external RBAC server
`storage.backups_volume`            | string    | local     | -                                 | Volume to use to store the backup tarballs (syntax is POOL/VOLUME)
`storage.images_volume`             | string    | local     | -                                 | Volume to use to store the image tarballs (syntax is POOL/VOLUME)

Those keys can be set using the `lxc` tool with:

```bash
lxc config set <key> <value>
```

When operating as part of a cluster, the keys marked with a `global`
scope will immediately be applied to all the cluster members. Those keys
with a `local` scope must be set on a per member basis using the
`--target` option of the command line tool.

## Exposing LXD to the network

By default, LXD can only be used by local users through a Unix socket.

To expose LXD to the network, you'll need to set `core.https_address`.
All remote clients can then connect to LXD and access any image which
was marked for public use.

Trusted clients can be manually added to the trust store on the server
with `lxc config trust add` or the `core.trust_password` key can be set
allowing for clients to self-enroll into the trust store at connection
time by providing the configured password.

More details about authentication can be found [here](security.md).

## External authentication

LXD when accessed over the network can be configured to use external
authentication through [Candid](https://github.com/canonical/candid).

Setting the `candid.*` configuration keys above to the values matching
your Candid deployment will allow users to authenticate through their
web browsers and then get trusted by LXD.

For those that have a Canonical RBAC server in front of their Candid
server, they can instead set the `rbac.*` configuration keys which are a
superset of the `candid.*` ones and allow for LXD to integrate with the
RBAC service.

When integrated with RBAC, individual users and groups can be granted
various level of access on a per-project basis. All of this is driven
externally through the RBAC service.

More details about authentication can be found [here](security.md).
