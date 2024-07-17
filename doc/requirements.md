(requirements)=
# Requirements

(requirements-go)=
## Go

LXD requires Go 1.22.5 or higher and is only tested with the Golang compiler.

We recommend having at least 2GiB of RAM to allow the build to complete.

## Kernel requirements

The minimum supported kernel version is 5.15, but older kernels should also work to some degree.

LXD requires a kernel with support for:

* Namespaces (`pid`, `net`, `uts`, `ipc` and `mount`)
* Seccomp
* Native Linux AIO
  ([`io_setup(2)`](https://man7.org/linux/man-pages/man2/io_setup.2.html), etc.)

The following optional features also require extra kernel options or newer versions:

* Namespaces (`user` and `cgroup`)
* AppArmor (including Ubuntu patch for mount mediation)
* Control Groups (`blkio`, `cpuset`, `devices`, `memory`, `pids` and `net_prio`)
* CRIU (exact details to be found with CRIU upstream)
* SKBPRIO/QFQ qdiscs (for `limits.priority`, minimum kernel 5.17)

As well as any other kernel feature required by the LXC version in use.

(requirements-lxc)=
## LXC

LXD requires LXC 5.0.0 or higher with the following build options:

* `apparmor` (if using LXD's AppArmor support)
* `seccomp`

To run recent version of various distributions, including Ubuntu, LXCFS
should also be installed.

## QEMU

For virtual machines, QEMU 6.2 or higher is required. Some features like
Confidential Guest support require a more recent QEMU and kernel version.

Hardware-assisted virtualization (Intel VT-x, AMD-V, etc) is required for
running virtual machines. Additional hardware support (Intel VT-d, AMD-Vi) may
be required for device pass-through.

(requirements-zfs)=
## ZFS

For the ZFS storage driver, ZFS 2.1 or higher is required. Some features
like `zfs_delegate` requires 2.2 or higher to be used.

## Additional libraries (and development headers)

LXD uses `dqlite` for its database, to build and set it up, you can
run `make deps`.

LXD itself also uses a number of (usually packaged) C libraries:

* `libacl1`
* `libcap2`
* `liblz4` (for `dqlite`)
* `libuv1` (for `dqlite`)
* `libsqlite3` >= 3.37.2 (for `dqlite`)

Make sure you have all these libraries themselves and their development
headers (`-dev` packages) installed.

## Related topics

{{getting_started_tut}}

{{getting_started_how}}
