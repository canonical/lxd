# Requirements
## Go

LXD requires Go 1.3 or higher (soon to become 1.4).
Both the golang and gccgo compilers are supported.

## Kernel requirements
The minimum supported kernel version is 3.13.

LXD requires a kernel with support for:
 * Namespaces (pid, net, uts, ipc, mount and user)
 * Control Groups (cpuset, cpuacct, devices, memory and net\_cls)
 * Seccomp

The following optional features also require extra kernel options:
 * AppArmor (including Ubuntu patch for mount mediation)
 * CRIU (exact details to be found with CRIU upstream)

As well as any other kernel feature required by the LXC version in use.

## LXC
LXD requires LXC 1.1.2 or higher with the following build options:
 * apparmor (if using LXD's apparmor support)
 * cgmanager
 * seccomp

To run recent version of various distributions, including Ubuntu, LXCFS
0.8 or higher should also be installed.
