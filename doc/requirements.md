# Requirements
## Go
LXD requires Go 1.18 or higher and is only tested with the Golang compiler.

We recommend having at least 2GB of RAM to allow the build to complete.

## Kernel requirements
The minimum supported kernel version is 5.4.

LXD requires a kernel with support for:

 * Namespaces (`pid`, `net`, `uts`, `ipc` and `mount`)
 * Seccomp

The following optional features also require extra kernel options:

 * Namespaces (`user` and `cgroup`)
 * AppArmor (including Ubuntu patch for mount mediation)
 * Control Groups (`blkio`, `cpuset`, `devices`, `memory`, `pids` and `net_prio`)
 * CRIU (exact details to be found with CRIU upstream)

As well as any other kernel feature required by the LXC version in use.

## LXC
LXD requires LXC 4.0.0 or higher with the following build options:

 * `apparmor` (if using LXD's AppArmor support)
 * `seccomp`

To run recent version of various distributions, including Ubuntu, LXCFS
should also be installed.

## QEMU
For virtual machines, QEMU 6.0 or higher is required.

## Additional libraries (and development headers)
LXD uses `dqlite` for its database, to build and set it up, you can
run `make deps`.

LXD itself also uses a number of (usually packaged) C libraries:

 - `libacl1`
 - `libcap2`
 - `liblz4` (for `dqlite`)
 - `libuv1` (for `dqlite`)
 - `libsqlite3` >= 3.25.0 (for `dqlite`)

Make sure you have all these libraries themselves and their development
headers (`-dev` packages) installed.
