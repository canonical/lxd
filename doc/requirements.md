# Requirements
## Go
LXD requires Go 1.10 or higher and is only tested with the golang compiler.

## Kernel requirements
The minimum supported kernel version is 3.13.

LXD requires a kernel with support for:

 * Namespaces (pid, net, uts, ipc and mount)
 * Seccomp

The following optional features also require extra kernel options:

 * Namespaces (user and cgroup)
 * AppArmor (including Ubuntu patch for mount mediation)
 * Control Groups (blkio, cpuset, devices, memory, pids and net\_prio)
 * CRIU (exact details to be found with CRIU upstream)

As well as any other kernel feature required by the LXC version in use.

## LXC
LXD requires LXC 2.0.0 or higher with the following build options:

 * apparmor (if using LXD's apparmor support)
 * seccomp

To run recent version of various distributions, including Ubuntu, LXCFS
should also be installed.

## Additional libraries (and development headers)
LXD uses `dqlite` for its database, to build and setup the custom
`sqlite3` and `dqlite` needed for it, you can run `make deps`.

LXD itself also uses a number of (usually packaged) C libraries:
 - libacl1
 - libcap2
 - libuv1 (for `dqlite`)

Make sure you have both the libraries themselves and their development
headers (-dev packages) installed.
