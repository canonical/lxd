(ref-projects)=
# Project configuration

Projects can be configured through a set of key/value configuration options.
See {ref}`projects-configure` for instructions on how to set these options.

The key/value configuration is namespaced.
The following options are available:

- {ref}`project-features`
- {ref}`project-limits`
- {ref}`project-restrictions`
- {ref}`project-specific-config`

(project-features)=
## Project features

The project features define which entities are isolated in the project and which are inherited from the `default` project.

If a `feature.*` option is set to `true`, the corresponding entity is isolated in the project.

```{note}
When you create a project without explicitly configuring a specific option, this option is set to the initial value given in the following table.

However, if you unset one of the `feature.*` options, it does not go back to the initial value, but to the default value.
The default value for all `feature.*` options is `false`.
```

Key                        | Type | Default | Initial value | Description
:--                        | :--  | :--     | :--           | :--
`features.images`          | bool | `false` | `true`        | Whether to use a separate set of images and image aliases for the project
`features.networks`        | bool | `false` | `false`       | Whether to use a separate set of networks for the project
`features.networks.zones`  | bool | `false` | `false`       | Whether to use a separate set of network zones for the project
`features.profiles`        | bool | `false` | `true`        | Whether to use a separate set of profiles for the project
`features.storage.buckets` | bool | `false` | `true`        | Whether to use a separate set of storage buckets for the project
`features.storage.volumes` | bool | `false` | `true`        | Whether to use a separate set of storage volumes for the project

(project-limits)=
## Project limits

Project limits define a hard upper bound for the resources that can be used by the containers and VMs that belong to a project.

Depending on the `limits.*` option, the limit applies to the number of entities that are allowed in the project (for example, `limits.containers` or `limits.networks`) or to the aggregate value of resource usage for all instances in the project (for example, `limits.cpu` or `limits.processes`).
In the latter case, the limit usually applies to the {ref}`instance-options-limits` that are configured for each instance (either directly or via a profile), and not to the resources that are actually in use.

For example, if you set the project's `limits.memory` configuration to `50GB`, the sum of the individual values of all `limits.memory` configuration keys defined on the project's instances will be kept under 50 GB.
If you try to create an instance that would make the total sum of `limits.memory` configurations exceed 50 GB, you will get an error.

Similarly, setting the project's `limits.cpu` configuration key to `100` means that the sum of individual `limits.cpu` values will be kept below 100.

When using project limits, the following conditions must be fulfilled:

- When you set one of the `limits.*` configurations and there is a corresponding configuration for the instance, all instances in the project must have the corresponding configuration defined (either directly or via a profile).
  See {ref}`instance-options-limits` for the instance configuration options.
- The `limits.cpu` configuration cannot be used if {ref}`instance-options-limits-cpu` is enabled.
  This means that to use `limits.cpu` on a project, the `limits.cpu` configuration of each instance in the project must be set to a number of CPUs, not a set or a range of CPUs.
- The `limits.memory` configuration must be set to an absolute value, not a percentage.

Key                       | Type    | Default | Description
:--                       | :--     | :--     | :--
`limits.containers`       | integer | -       | Maximum number of containers that can be created in the project
`limits.cpu`              | integer | -       | Maximum value for the sum of individual `limits.cpu` configurations set on the instances of the project
`limits.disk`             | string  | -       | Maximum value of aggregate disk space used by all instance volumes, custom volumes, and images of the project
`limits.instances`        | integer | -       | Maximum number of total instances that can be created in the project
`limits.memory`           | string  | -       | Maximum value for the sum of individual `limits.memory` configurations set on the instances of the project
`limits.networks`         | integer | -       | Maximum number of networks that the project can have
`limits.processes`        | integer | -       | Maximum value for the sum of individual `limits.processes` configurations set on the instances of the project
`limits.virtual-machines` | integer | -       | Maximum number of VMs that can be created in the project

(project-restrictions)=
## Project restrictions

To prevent the instances of a project from accessing security-sensitive features (such as container nesting or raw LXC configuration), set the `restricted` configuration option to `true`.
You can then use the various `restricted.*` options to pick individual features that would normally be blocked by `restricted` and allow them, so they can be used by the instances of the project.

For example, to restrict a project and block all security-sensitive features, but allow container nesting, enter the following commands:

    lxc project set <project_name> restricted=true
    lxc project set <project_name> restricted.containers.nesting=allow

Each security-sensitive feature has an associated `restricted.*` project configuration option.
If you want to allow the usage of a feature, change the value of its `restricted.*` option.
Most `restricted.*` configurations are binary switches that can be set to either `block` (the default) or `allow`.
However, some options support other values for more fine-grained control.

```{note}
You must set the `restricted` configuration to `true` for any of the `restricted.*` options to be effective.
If `restricted` is set to `false`, changing a `restricted.*` option has no effect.

Setting all `restricted.*` keys to `allow` is equivalent to setting `restricted` itself to `false`.
```

Key                                    | Type   | Default        | Description
:--                                    | :--    | :--            | :--
`restricted`                           | bool   | `false`        | Whether to block access to security-sensitive features - must be enabled to allow the `restricted.*` keys to take effect (this is so it can be temporarily disabled if needed without having to clear the related keys)
`restricted.backups`                   | string | `block`        | Prevents creating any instance or volume backups
`restricted.cluster.groups`            | string | -              | Prevents targeting cluster groups other than the provided ones
`restricted.cluster.target`            | string | `block`        | Prevents direct targeting of cluster members when creating or moving instances
`restricted.containers.lowlevel`       | string | `block`        | Prevents using low-level container options like `raw.lxc`, `raw.idmap`, `volatile`, etc.
`restricted.containers.nesting`        | string | `block`        | Prevents setting `security.nesting=true`
`restricted.containers.privilege`      | string | `unprivileged` | Prevents configuring privileged containers (`unpriviliged` prevents setting `security.privileged=true`, `isolated` prevents setting `security.privileged=true` and also `security.idmap.isolated=true`, `allow` means no restrictions)
`restricted.containers.interception`   | string | `block`        | Prevents using system call interception options - when set to `allow`, usually safe interception options will be allowed (file system mounting will remain blocked)
`restricted.devices.disk`              | string | `managed`      | Prevents using disk devices (`block` prevents using disk devices except the root one, `managed` allows using disk devices only if `pool=` is set, `allow` means no restrictions)
`restricted.devices.disk.paths`        | string | -              | If `restricted.devices.disk` is set to `allow`: a comma-separated list of path prefixes that restrict the `source` setting on `disk` devices (if empty, all paths are allowed)
`restricted.devices.gpu`               | string | `block`        | Prevents using devices of type `gpu`
`restricted.devices.infiniband`        | string | `block`        | Prevents using devices of type `infiniband`
`restricted.devices.nic`               | string | `managed`      | Prevents using network devices and controls access to networks (`block` prevents using all network devices, `managed` allows using network devices only if `network=` is set, `allow` means no restrictions)
`restricted.devices.pci`               | string | `block`        | Prevents using devices of type `pci`
`restricted.devices.proxy`             | string | `block`        | Prevents using devices of type `proxy`
`restricted.devices.unix-block`        | string | `block`        | Prevents using devices of type `unix-block`
`restricted.devices.unix-char`         | string | `block`        | Prevents using devices of type `unix-char`
`restricted.devices.unix-hotplug`      | string | `block`        | Prevents using devices of type `unix-hotplug`
`restricted.devices.usb`               | string | `block`        | Prevents using devices of type `usb`
`restricted.idmap.uid`                 | string | -              | Specifies the host UID ranges allowed in the instance's `raw.idmap` setting
`restricted.idmap.gid`                 | string | -              | Specifies the host GID ranges allowed in the instance's `raw.idmap` setting
`restricted.networks.access`           | string | -              | Comma-delimited list of network names that are allowed for use in this project - if not set, all networks are accessible (this setting depends on the `restricted.devices.nic` setting)
`restricted.networks.subnets`          | string | `block`        | Comma-delimited list of network subnets from the uplink networks (in the form `<uplink>:<subnet>`) that are allocated for use in this project
`restricted.networks.uplinks`          | string | `block`        | Comma-delimited list of network names that can be used as uplink for networks in this project
`restricted.networks.zones`            | string | `block`        | Comma-delimited list of network zones that can be used (or something under them) in this project
`restricted.snapshots`                 | string | `block`        | Prevents creating any instance or volume snapshots
`restricted.virtual-machines.lowlevel` | string | `block`        | Prevents using low-level VM options like `raw.qemu`, `volatile`, etc.

(project-specific-config)=
## Project-specific configuration

There are some {ref}`server` options that you can override for a project.
In addition, you can add user metadata for a project.

Key                             | Type    | Default | Description
:--                             | :--     | :--     | :--
`backups.compression_algorithm` | string  | -       | Compression algorithm to use for backups (`bzip2`, `gzip`, `lzma`, `xz`, or `none`) in the project
`images.auto_update_cached`     | bool    | -       | Whether to automatically update any image that LXD caches
`images.auto_update_interval`   | integer | -       | Interval (in hours) at which to look for updates to cached images (`0` to disable)
`images.compression_algorithm`  | string  | -       | Compression algorithm to use for new images (`bzip2`, `gzip`, `lzma`, `xz`, or `none`) in the project
`images.default_architecture`   | string  | -       | Default architecture to use in a mixed-architecture cluster
`images.remote_cache_expiry`    | integer | -       | Number of days after which an unused cached remote image is flushed in the project
`user.*`                        | string  | -       | User-provided free-form key/value pairs
