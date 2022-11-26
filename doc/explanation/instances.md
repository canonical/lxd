---
discourse: 8767,7519,9281
relatedlinks: https://ubuntu.com/blog/lxd-virtual-machines-an-overview
---

(expl-instances)=
# About instances

LXD supports the following types of instances:

Containers
: Containers are the default type for instances.
  They are currently the most complete implementation of LXD instances and support more features than virtual machines.

  Containers are implemented through the use of `liblxc` (LXC).

Virtual machines
: {abbr}`Virtual machines (VMs)` are natively supported since version 4.0 of LXD.
  Thanks to a built-in agent, they can be used almost like containers.

  LXD uses `qemu` to provide the VM functionality.

  ```{note}
  Currently, virtual machines support fewer features than containers, but the plan is to support the same set of features for both instance types in the future.

  To see which features are available for virtual machines, check the condition column in the {ref}`instance-options` documentation.
  ```
