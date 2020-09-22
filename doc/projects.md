# Project configuration
LXD supports projects as a way to split your LXD server.
Each project holds its own set of instances and may also have its own images and profiles.

What a project contains is defined through the `features` configuration keys.
When a feature is disabled, the project inherits from the `default` project.

By default all new projects get the entire feature set, on upgrade,
existing projects do not get new features enabled.

The key/value configuration is namespaced with the following namespaces
currently supported:

 - `features` (What part of the project featureset is in use)
 - `limits` (Resource limits applied on containers and VMs belonging to the project)
 - `user` (free form key/value for user metadata)

Key                                  | Type      | Condition             | Default                   | Description
:--                                  | :--       | :--                   | :--                       | :--
features.images                      | boolean   | -                     | true                      | Separate set of images and image aliases for the project
features.networks                    | boolean   | -                     | true                      | Separate set of networks for the project
features.profiles                    | boolean   | -                     | true                      | Separate set of profiles for the project
features.storage.volumes             | boolean   | -                     | true                      | Separate set of storage volumes for the project
limits.containers                    | integer   | -                     | -                         | Maximum number of containers that can be created in the project
limits.cpu                           | integer   | -                     | -                         | Maximum value for the sum of individual "limits.cpu" configs set on the instances of the project
limits.disk                          | string    | -                     | -                         | Maximum value of aggregate disk space used by all instances volumes, custom volumes and images of the project
limits.memory                        | string    | -                     | -                         | Maximum value for the sum of individual "limits.memory" configs set on the instances of the project
limits.networks                      | integer   | -                     | -                         | Maximum value for the number of networks this project can have
limits.processes                     | integer   | -                     | -                         | Maximum value for the sum of individual "limits.processes" configs set on the instances of the project
limits.virtual-machines              | integer   | -                     | -                         | Maximum number of VMs that can be created in the project
restricted                           | boolean   | -                     | true                      | Block access to security-sensitive features
restricted.containers.lowlevel       | string    | -                     | block                     | Prevents use of low-level container options like raw.lxc, raw.idmap, volatile, etc.
restricted.containers.nesting        | string    | -                     | block                     | Prevents setting security.nesting=true.
restricted.containers.privilege      | string    | -                     | unpriviliged              | If "unpriviliged", prevents setting security.privileged=true. If "isolated", prevents setting security.privileged=true and also security.idmap.isolated=true. If "allow", no restriction apply.
restricted.devices.disk              | string    | -                     | managed                   | If "block" prevent use of disk devices except the root one. If "managed" allow use of disk devices only if "pool=" is set. If "allow", no restrictions apply.
restricted.devices.gpu               | string    | -                     | block                     | Prevents use of devices of type "gpu"
restricted.devices.infiniband        | string    | -                     | block                     | Prevents use of devices of type "infiniband"
restricted.devices.nic               | string    | -                     | managed                   | If "block" prevent use of all network devices. If "managed" allow use of network devices only if "network=" is set. If "allow", no restrictions apply.
restricted.devices.unix-block        | string    | -                     | block                     | Prevents use of devices of type "unix-block"
restricted.devices.unix-char         | string    | -                     | block                     | Prevents use of devices of type "unix-char"
restricted.devices.unix-hotplug      | string    | -                     | block                     | Prevents use of devices of type "unix-hotplug"
restricted.devices.usb               | string    | -                     | block                     | Prevents use of devices of type "usb"
restricted.virtual-machines.lowlevel | string    | -                     | block                     | Prevents use of low-level virtual-machine options like raw.qemu, volatile, etc.

Those keys can be set using the lxc tool with:

```bash
lxc project set <project> <key> <value>
```

## Project limits

Note that to be able to set one of the `limits.*` config keys, **all** instances
in the project **must** have that same config key defined, either directly or
via a profile.

In addition to that:

- The `limits.cpu` config key also requires that CPU pinning is **not** used.
- The `limits.memory` config key must be set to an absolute value, **not** a percentage.

The `limits.*` config keys defined on a project act as a hard upper bound for
the **aggregate** value of the individual `limits.*` config keys defined on the
project's instances, either directly or via profiles.

For example, setting the project's `limits.memory` config key to `50GB` means
that the sum of the individual values of all `limits.memory` config keys defined
on the project's instances will be kept under `50GB`. Trying to create or modify
an instance assigning it a `limits.memory` value that would make the total sum
exceed `50GB`, will result in an error.

Similarly, setting the project's `limits.cpu` config key to `100`, means that
the **sum** of individual `limits.cpu` values will be kept below `100`.

## Project restrictions

If the `restricted` config key is set to `true`, then the instances of the
project won't be able to access security-sensitive features, such as container
nesting, raw LXC configuration, etc.

The exact set of features that the `restricted` config key blocks may grow
across LXD releases, as more features are added that are considered
security-sensitive.

Using the various `restricted.*` sub-keys, it's possible to pick individual
features which would be normally blocked by `restricted` and white-list them, so
they can be used by instances of the project.

For example:

```bash
lxc project set <project> restricted=true
lxc project set <project> restricted.containers.nesting=allow
```

will block all security-sensitive features **except** container nesting.

Each security-sensitive feature has an associated `restricted.*` project config
sub-key whose default value needs to be explicitly changed if you want for that
feature to be white-listed and allow it in the project.

Note that changing the value of a specific `restricted.*` config key has an
effect only if the top-level `restricted` key itself is currently set to
`true`. If `restricted` is set to `false`, changing a `restricted.*` sub-key is
effectively a no-op.

Most `'restricted.*` config keys are binary switches that can be set to either
`block` (the default) or `allow`. However some of them support other values for
more fine-grained control.

Setting all `restricted.*` keys to `allow` is effectively equivalent to setting
`restricted` itself to `false`.
