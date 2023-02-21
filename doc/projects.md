---
relatedlinks: https://www.youtube.com/watch?v=6O0q3rSWr8A, https://ubuntu.com/tutorials/introduction-to-lxd-projects
---

(projects)=
# Projects

```{youtube} https://www.youtube.com/watch?v=cUHkgg6TovM
```

LXD supports projects as a way to split your LXD server.
Each project holds its own set of instances and may also have its own images and profiles.

What a project contains is defined through the `features` configuration keys.
When a feature is disabled, the project inherits from the `default` project.

By default all new projects get the entire feature set, on upgrade,
existing projects do not get new features enabled.

The key/value configuration is namespaced with the following namespaces
currently supported:

- `features` (What part of the project feature set is in use)
- `limits` (Resource limits applied on containers and VMs belonging to the project)
- `user` (free form key/value for user metadata)

Key                                  | Type      | Condition             | Default                   | Description
:--                                  | :--       | :--                   | :--                       | :--
`backups.compression_algorithm`      | string    | -                     | -                         | Compression algorithm to use for backups (`bzip2`, `gzip`, `lzma`, `xz` or `none`) in the project
`features.images`                    | bool      | -                     | `true`                    | Separate set of images and image aliases for the project
`features.networks`                  | bool      | -                     | `false`                   | Separate set of networks for the project
`features.networks.zones`            | bool      | -                     | `false`                   | Separate set of network zones for the project
`features.profiles`                  | bool      | -                     | `true`                    | Separate set of profiles for the project
`features.storage.buckets`           | bool      | -                     | `true`                    | Separate set of storage buckets for the project
`features.storage.volumes`           | bool      | -                     | `true`                    | Separate set of storage volumes for the project
`images.auto_update_cached`          | bool      | -                     | -                         | Whether to automatically update any image that LXD caches
`images.auto_update_interval`        | integer   | -                     | -                         | Interval in hours at which to look for update to cached images (0 disables it)
`images.compression_algorithm`       | string    | -                     | -                         | Compression algorithm to use for images (`bzip2`, `gzip`, `lzma`, `xz` or `none`) in the project
`images.default_architecture`        | string    | -                     | -                         | Default architecture which should be used in mixed architecture cluster
`images.remote_cache_expiry`         | integer   | -                     | -                         | Number of days after which an unused cached remote image will be flushed in the project
`limits.containers`                  | integer   | -                     | -                         | Maximum number of containers that can be created in the project
`limits.cpu`                         | integer   | -                     | -                         | Maximum value for the sum of individual `limits.cpu` configurations set on the instances of the project
`limits.disk`                        | string    | -                     | -                         | Maximum value of aggregate disk space used by all instances volumes, custom volumes and images of the project
`limits.instances`                   | integer   | -                     | -                         | Maximum number of total instances that can be created in the project
`limits.memory`                      | string    | -                     | -                         | Maximum value for the sum of individual `limits.memory` configurations set on the instances of the project
`limits.networks`                    | integer   | -                     | -                         | Maximum value for the number of networks this project can have
`limits.processes`                   | integer   | -                     | -                         | Maximum value for the sum of individual `limits.processes` configurations set on the instances of the project
`limits.virtual-machines`            | integer   | -                     | -                         | Maximum number of VMs that can be created in the project
`restricted`                         | bool      | -                     | `false`                   | Block access to security-sensitive features (this must be enabled to allow the `restricted.*` keys to take effect, this is so it can be temporarily disabled if needed without having to clear the related keys)
`restricted.backups`                 | string    | -                     | `block`                   | Prevents the creation of any instance or volume backups.
`restricted.cluster.groups`          | string    | -                     | -                         | Prevents targeting cluster groups other than the provided ones.
`restricted.cluster.target`          | string    | -                     | `block`                   | Prevents direct targeting of cluster members when creating or moving instances.
`restricted.containers.lowlevel`     | string    | -                     | `block`                   | Prevents use of low-level container options like `raw.lxc`, `raw.idmap`, `volatile` etc.
`restricted.containers.nesting`      | string    | -                     | `block`                   | Prevents setting `security.nesting=true`.
`restricted.containers.privilege`    | string    | -                     | `unpriviliged`            | If `unpriviliged`, prevents setting `security.privileged=true`. If `isolated`, prevents setting `security.privileged=true` and also `security.idmap.isolated=true`. If `allow`, no restriction apply.
`restricted.containers.interception` | string    | -                     | `block`                   | Prevents use for system call interception options. When set to `allow` usually safe interception options will be allowed (file system mounting will remain blocked).
`restricted.devices.disk`            | string    | -                     | `managed`                 | If `block` prevent use of disk devices except the root one. If `managed` allow use of disk devices only if `pool=` is set. If `allow`, no restrictions apply.
`restricted.devices.disk.paths`      | string    | -                     | -                         | If `restricted.devices.disk` is set to `allow`, this sets a comma-separated list of path prefixes that restrict the `source` setting on `disk` devices. If empty then all paths are allowed.
`restricted.devices.gpu`             | string    | -                     | `block`                   | Prevents use of devices of type `gpu`
`restricted.devices.infiniband`      | string    | -                     | `block`                   | Prevents use of devices of type `infiniband`
`restricted.devices.nic`             | string    | -                     | `managed`                 | If `block` prevent use of all network devices. If `managed` allow use of network devices only if `network=` is set. If `allow`, no restrictions apply. This also controls access to networks.
`restricted.devices.pci`             | string    | -                     | `block`                   | Prevents use of devices of type `pci`
`restricted.devices.proxy`           | string    | -                     | `block`                   | Prevents use of devices of type `proxy`
`restricted.devices.unix-block`      | string    | -                     | `block`                   | Prevents use of devices of type `unix-block`
`restricted.devices.unix-char`       | string    | -                     | `block`                   | Prevents use of devices of type `unix-char`
`restricted.devices.unix-hotplug`    | string    | -                     | `block`                   | Prevents use of devices of type `unix-hotplug`
`restricted.devices.usb`             | string    | -                     | `block`                   | Prevents use of devices of type `usb`
`restricted.idmap.uid`               | string    | -                     | -                         | Specifies the allowed host UID ranges allowed in the instance `raw.idmap` setting.
`restricted.idmap.gid`               | string    | -                     | -                         | Specifies the allowed host GID ranges allowed in the instance `raw.idmap` setting.
`restricted.networks.access`         | string    | -                     | -                         | Comma-delimited list of network names that are allowed for use in this project. If not set, all networks are accessible (depending on the `restricted.devices.nic` setting).
`restricted.networks.subnets`        | string    | -                     | `block`                   | Comma-delimited list of network subnets from the uplink networks (in the form `<uplink>:<subnet>`) that are allocated for use in this project
`restricted.networks.uplinks`        | string    | -                     | `block`                   | Comma-delimited list of network names that can be used as uplink for networks in this project
`restricted.networks.zones`          | string    | -                     | `block`                   | Comma-delimited list of network zones that can be used (or something under them) in this project
`restricted.snapshots`               | string    | -                     | `block`                   | Prevents the creation of any instance or volume snapshots.
`restricted.virtual-machines.lowlevel`| string   | -                     | `block`                   | Prevents use of low-level virtual-machine options like `raw.qemu`, `volatile` etc.

Those keys can be set using the `lxc` tool with:

```bash
lxc project set <project> <key> <value>
```

## Project limits

Note that to be able to set one of the `limits.*` configuration keys, **all** instances
in the project **must** have that same configuration key defined, either directly or
via a profile.

In addition to that:

- The `limits.cpu` configuration key also requires that CPU pinning is **not** used.
- The `limits.memory` configuration key must be set to an absolute value, **not** a percentage.

The `limits.*` configuration keys defined on a project act as a hard upper bound for
the **aggregate** value of the individual `limits.*` configuration keys defined on the
project's instances, either directly or via profiles.

For example, setting the project's `limits.memory` configuration key to `50GB` means
that the sum of the individual values of all `limits.memory` configuration keys defined
on the project's instances will be kept under `50GB`. Trying to create or modify
an instance assigning it a `limits.memory` value that would make the total sum
exceed `50GB`, will result in an error.

Similarly, setting the project's `limits.cpu` configuration key to `100`, means that
the **sum** of individual `limits.cpu` values will be kept below `100`.

(projects-restrictions)=
## Project restrictions

If the `restricted` configuration key is set to `true`, then the instances of the
project won't be able to access security-sensitive features, such as container
nesting, raw LXC configuration, etc.

The exact set of features that the `restricted` configuration key blocks may grow
across LXD releases, as more features are added that are considered
security-sensitive.

Using the various `restricted.*` sub-keys, it's possible to pick individual
features which would be normally blocked by `restricted` and allow them, so
they can be used by instances of the project.

For example:

```bash
lxc project set <project> restricted=true
lxc project set <project> restricted.containers.nesting=allow
```

will block all security-sensitive features **except** container nesting.

Each security-sensitive feature has an associated `restricted.*` project configuration
sub-key whose default value needs to be explicitly changed if you want for that
feature to be allowed it in the project.

Note that changing the value of a specific `restricted.*` configuration key has an
effect only if the top-level `restricted` key itself is currently set to
`true`. If `restricted` is set to `false`, changing a `restricted.*` sub-key is
effectively a no-op.

Most `'restricted.*` configuration keys are binary switches that can be set to either
`block` (the default) or `allow`. However some of them support other values for
more fine-grained control.

Setting all `restricted.*` keys to `allow` is effectively equivalent to setting
`restricted` itself to `false`.
