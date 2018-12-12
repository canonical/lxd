# Server configuration
The server configuration is a simple set of key and values.

The key/value configuration is namespaced with the following namespaces
currently supported:

 - `core` (core daemon configuration)
 - `images` (image configuration)
 - `maas` (MAAS integration)

Key                                 | Type      | Default   | API extension                     | Description
:--                                 | :---      | :------   | :------------                     | :----------
backups.compression\_algorithm      | string    | gzip      | backup\_compression               | Compression algorithm to use for new images (bzip2, gzip, lzma, xz or none)
candid.api.key                      | string    | -         | candid\_config\_key               | Public key of the candid server (required for HTTP-only servers)
candid.api.url                      | string    | -         | candid\_authentication            | URL of the the external authentication endpoint using Candid
candid.expiry                       | integer   | 3600      | candid\_config                    | Candid macaroon expiry in seconds
candid.domains                      | string    | -         | candid\_config                    | Comma-separated list of allowed Candid domains (empty string means all domains are valid)
cluster.https\_address              | string    | -         | clustering\_server\_address       | Address the server should using for clustering traffic
cluster.offline\_threshold          | integer   | 20        | clustering                        | Number of seconds after which an unresponsive node is considered offline
cluster.images\_minimal\_replica    | integer   | 3         | clustering\_image\_replication    | Minimal numbers of cluster members with a copy of a particular image (set 1 for no replication, -1 for all members)
core.debug\_address                 | string    | -         | pprof\_http                       | Address to bind the pprof debug server to (HTTP)
core.https\_address                 | string    | -         | -                                 | Address to bind for the remote API (HTTPs)
core.https\_allowed\_credentials    | boolean   | -         | -                                 | Whether to set Access-Control-Allow-Credentials http header value to "true"
core.https\_allowed\_headers        | string    | -         | -                                 | Access-Control-Allow-Headers http header value
core.https\_allowed\_methods        | string    | -         | -                                 | Access-Control-Allow-Methods http header value
core.https\_allowed\_origin         | string    | -         | -                                 | Access-Control-Allow-Origin http header value
core.proxy\_https                   | string    | -         | -                                 | https proxy to use, if any (falls back to HTTPS\_PROXY environment variable)
core.proxy\_http                    | string    | -         | -                                 | http proxy to use, if any (falls back to HTTP\_PROXY environment variable)
core.proxy\_ignore\_hosts           | string    | -         | -                                 | hosts which don't need the proxy for use (similar format to NO\_PROXY, e.g. 1.2.3.4,1.2.3.5, falls back to NO\_PROXY environment variable)
core.trust\_password                | string    | -         | -                                 | Password to be provided by clients to setup a trust
images.auto\_update\_cached         | boolean   | true      | -                                 | Whether to automatically update any image that LXD caches
images.auto\_update\_interval       | integer   | 6         | -                                 | Interval in hours at which to look for update to cached images (0 disables it)
images.compression\_algorithm       | string    | gzip      | -                                 | Compression algorithm to use for new images (bzip2, gzip, lzma, xz or none)
images.remote\_cache\_expiry        | integer   | 10        | -                                 | Number of days after which an unused cached remote image will be flushed
maas.api.key                        | string    | -         | maas\_network                     | API key to manage MAAS
maas.api.url                        | string    | -         | maas\_network                     | URL of the MAAS server
maas.machine                        | string    | hostname  | maas\_network                     | Name of this LXD host in MAAS

Those keys can be set using the lxc tool with:

```bash
lxc config set <key> <value>
```
