# Server configuration
The key/value configuration is namespaced with the following namespaces
currently supported:

 - `core` (core daemon configuration)
 - `images` (image configuration)
 - `storage` (storage configuration)

Key                             | Type          | Default                   | Description
:--                             | :---          | :------                   | :----------
core.https\_address             | string        | -                         | Address to bind for the remote API
core.https\_allowed\_headers    | string        | -                         | Access-Control-Allow-Headers http header value
core.https\_allowed\_methods    | string        | -                         | Access-Control-Allow-Methods http header value
core.https\_allowed\_origin     | string        | -                         | Access-Control-Allow-Origin http header value
core.proxy\_https               | string        | -                         | https proxy to use, if any (falls back to HTTPS\_PROXY environment variable)
core.proxy\_http                | string        | -                         | http proxy to use, if any (falls back to HTTP\_PROXY environment variable)
core.proxy\_ignore\_hosts       | string        | -                         | hosts which don't need the proxy for use (similar format to NO\_PROXY, e.g. 1.2.3.4,1.2.3.5, falls back to NO\_PROXY environment variable)
core.trust\_password            | string        | -                         | Password to be provided by clients to setup a trust
images.auto\_update\_cached     | boolean       | true                      | Whether to automatically update any image that LXD caches
images.auto\_update\_interval   | integer       | 6                         | Interval in hours at which to look for update to cached images (0 disables it)
images.compression\_algorithm   | string        | gzip                      | Compression algorithm to use for new images (bzip2, gzip, lzma, xz or none)
images.remote\_cache\_expiry    | integer       | 10                        | Number of days after which an unused cached remote image will be flushed
storage.lvm\_fstype             | string        | ext4                      | Format LV with filesystem, for now it's value can be only ext4 (default) or xfs.
storage.lvm\_thinpool\_name     | string        | "LXDPool"                 | LVM Thin Pool to use within the Volume Group specified in `storage.lvm_vg_name`, if the default pool parameters are undesirable.
storage.lvm\_vg\_name           | string        | -                         | LVM Volume Group name to be used for container and image storage. A default Thin Pool is created using 100% of the free space in the Volume Group, unless `storage.lvm_thinpool_name` is set.
storage.lvm\_volume\_size       | string        | 10GiB                     | Size of the logical volume
storage.zfs\_pool\_name         | string        | -                         | ZFS pool name

Those keys can be set using the lxc tool with:

```bash
lxc config set <key> <value>
```
