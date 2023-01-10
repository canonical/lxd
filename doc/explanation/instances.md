---
discourse: 8767,7519,9281,9223
---

(expl-instances)=
# About instances

## Containers

Containers are the default type for LXD and currently the most
complete implementation of LXD instances, providing the most features.

They are implemented through the use of `liblxc` (LXC).

There is limited support for {ref}`live-migration`.

# Virtual Machines

Virtual machines are an instance type supported by LXD alongside containers.

They are implemented through the use of `qemu`.

Please note, currently not all features that are available with containers have been implemented for VMs,
however we continue to strive for feature parity with containers.

## Configuration

See [instance configuration](instances.md) for valid configuration options.
