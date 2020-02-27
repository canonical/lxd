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

Key                             | Type      | Condition             | Default                   | Description
:--                             | :--       | :--                   | :--                       | :--
features.images                 | boolean   | -                     | true                      | Separate set of images and image aliases for the project
features.profiles               | boolean   | -                     | true                      | Separate set of profiles for the project
limits.containers               | integer   | -                     | -                         | Maximum number of containers that can be created in the project
limits.virtual-machines         | integer   | -                     | -                         | Maximum number of VMs that can be created in the project
limits.cpu                      | integer   | -                     | -                         | Maximum value for the sum of individual "limits.cpu" configs set on the instances of the project
limits.memory                   | integer   | -                     | -                         | Maximum value for the sum of individual "limits.memory" configs set on the instances of the project
limits.processes                | integer   | -                     | -                         | Maximum value for the sum of individual "limits.processes" configs set on the instances of the project

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
