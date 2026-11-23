(requirements)=
# Requirements

To run LXD, you must satisfy certain minimum requirements:

- The {ref}`host requirements <requirements-host>` apply to all installations of LXD, regardless of installation method.
- The {ref}`runtime dependencies <requirements-runtime>` are bundled in the {ref}`LXD snap package <installing-snap-package>`, and updated when the snap refreshes.
  If you install LXD through a {ref}`different method <installing-other>`, you must install these dependencies at or above the minimum versions listed below and manage updates yourself.

To build LXD from source, you must also meet the {ref}`build requirements <requirements-build>`.

(requirements-host)=
## Host requirements

These requirements apply to all installations of LXD, including the snap.

(requirements-kernel)=
### Kernel

The minimum supported kernel version is 6.8.

LXD requires a kernel with support for:

- Namespaces (`pid`, `net`, `uts`, `ipc` and `mount`)
- Seccomp
- Native Linux AIO ([`io_setup(2)`](https://man7.org/linux/man-pages/man2/io_setup.2.html), and so on)
- Any other kernel feature required by the LXC version in use

The following optional features also require extra kernel options or newer versions:

- Namespaces (`user` and `cgroup`)
- AppArmor (including Ubuntu patch for mount mediation), strongly recommended for production deployments (see {ref}`howto-security-harden-apparmor`)
- Control groups (`blkio`, `cpuset`, `devices`, `memory`, `pids` and `net_prio`)
- CRIU (see upstream CRIU for details)
- SKBPRIO/QFQ qdiscs (for `limits.priority`, minimum kernel 5.17)
- ZFS kernel module (for the {ref}`ZFS storage driver <storage-zfs>`), version 2.2 or higher

(requirements-virtualization)=
### Hardware virtualization

Hardware-assisted virtualization (for example, Intel VT-x or AMD-V) is required for running virtual machines.
Additional hardware support (for example, Intel VT-d or AMD-Vi) might be required for device pass-through.

(requirements-runtime)=
## Runtime dependencies

The following dependencies are required to run LXD.
In {ref}`snap installations <installing-snap-package>`, these dependencies are bundled in the snap and updated when the snap refreshes.
If you use a {ref}`different installation method <installing-other>`, you must install and manage these dependencies yourself.

(requirements-lxc)=
### LXC

LXD requires LXC 5.0.0 or higher with the following build options:

- `apparmor` (if using LXD's AppArmor support)
- `seccomp`

To run recent versions of various distributions, including Ubuntu, LXCFS should also be installed.

(requirements-qemu)=
### QEMU

Virtual machines require QEMU 8.2.2 or higher and `virtiofsd` 1.10.0 or higher.
Some features like Confidential Guest support require a more recent QEMU and kernel version.

### Dqlite

LXD uses Dqlite for its database, as detailed in {ref}`database`.

(requirements-zfs-tools)=
### ZFS userspace tools

The {ref}`ZFS storage driver <storage-zfs>` requires the ZFS userspace tools, version 2.2 or higher.
The userspace tools must match the major and minor version of the ZFS kernel module on the host (see {ref}`requirements-kernel`).

For snap installations, if none of the versions of the userspace tools bundled in the snap match the ZFS kernel module, you must install the userspace tools and {ref}`configure the snap <howto-snap-configure>` by running:

```bash
sudo snap set lxd zfs.external=true
```

(requirements-libraries)=
### Libraries

LXD, LXC, and Dqlite use a number of (usually packaged) C libraries:

- `libacl`
- `libapparmor`
- `libcap`
- `liblz4`
- `libseccomp`
- `libsqlite3` 3.37.2 or higher
- `libuv`

(requirements-runtime-deps)=
### Additional runtime dependencies

LXD might require the following packages at runtime:

- `attr`
- `iproute2`
- `nftables` (or `iptables`)
- `rsync`
- `squashfs-tools`
- `squashfs-tools-ng`
- `tar`
- `xz-utils`

(requirements-build)=
## Build requirements

To build LXD from source, you must also meet the requirements below.

For instructions on installing these dependencies on Ubuntu, see {ref}`installing-from-source`.

(requirements-go)=
### Go

LXD requires Go 1.27.4 or higher and is only tested with the Golang compiler.

We recommend having at least 2 GiB of RAM to allow the build to complete.

(requirements-build-toolchain)=
### Build toolchain

Building LXD from source requires the following packages:

- `autoconf`
- `automake`
- `build-essential`
- `git`
- `libtool`
- `make`
- `meson`
- `ninja-build`
- `pkg-config`
- `python3-venv` (for the documentation)

(requirements-build-deps)=
### Development headers

The development headers for the {ref}`runtime libraries <requirements-libraries>` discussed above are required to build LXD, LXC, and Dqlite.
Building Dqlite also requires the `xfslibs-dev` header.

## Related topics

{{getting_started_tut}}

{{getting_started_how}}
