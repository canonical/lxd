---
discourse: lxc:[Overview&#32;-&#32;GUI&#32;inside&#32;Containers](8767),lxc:[Running&#32;virtual&#32;machines&#32;with&#32;LXD&#32;4.0](7519),lxc:[Install&#32;any&#32;OS&#32;via&#32;ISO&#32;in&#32;a&#32;Virtual&#32;machine/VM](9281)
relatedlinks: "[LXD&#32;virtual&#32;machines:&#32;an&#32;overview](https://ubuntu.com/blog/lxd-virtual-machines-an-overview)"
---

(containers-and-vms)=
# Containers and VMs

LXD provides support for two different types of {ref}`instances <expl-instances>`: *system containers* and *virtual machines*.

When running a system container, LXD simulates a virtual version of a full operating system. To do this, it uses the functionality provided by the kernel running on the host system.

When running a virtual machine, LXD uses the hardware of the host system, but the kernel is provided by the virtual machine. Therefore, virtual machines can be used to run, for example, a different operating system.

## Application containers vs. system containers

Application containers (as provided by, for example, Docker) package a single process or application. System containers, on the other hand, simulate a full operating system and let you run multiple processes at the same time.

Therefore, application containers are suitable to provide separate components, while system containers provide a full solution of libraries, applications, databases, and so on. In addition, you can use system containers to create different user spaces and isolate all processes belonging to each user space, which is not what application containers are intended for.

![Application and system containers](/images/application-vs-system-containers.svg "Application and system containers")

## Virtual machines vs. system containers

Virtual machines emulate a physical machine, using the hardware of the host system from a full and completely isolated operating system. System containers, on the other hand, use the OS kernel of the host system instead of creating their own environment. If you run several system containers, they all share the same kernel, which makes them faster and more lightweight than virtual machines.

With LXD, you can create both system containers and virtual machines. You should use a system container to leverage the smaller size and increased performance if all functionality you require is compatible with the kernel of your host operating system. If you need functionality that is not supported by the OS kernel of your host system or you want to run a completely different OS, use a virtual machine.

![Virtual machines and system containers](/images/virtual-machines-vs-system-containers.svg "Virtual machines and system containers")

(expl-instances)=
## Instance types in LXD

LXD supports the following types of instances:

Containers
: Containers are the default type for instances. They are implemented through the use of `liblxc` (LXC).

Virtual machines
: {abbr}`Virtual machines (VMs)` are natively supported since version 4.0 of LXD.
  Thanks to a built-in agent, they can be used almost like containers, with a similar set of features.

  LXD uses `qemu` to provide the VM functionality.

  ```{note}
  In the {ref}`instance-options` documentation, some instance options display a `condition` field in their details, with the value of either `container` or `virtual machine`. This indicates the type of instance for which that option is available. If no `condition` field exists in an option's details, that option applies to both types.
  ```

## Related topics

{{instances_how}}

{{instances_ref}}
