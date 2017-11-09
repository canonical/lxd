# Container runtime environment
LXD attempts to present a consistent environment to the container it runs.

The exact environment will differ slightly based on kernel features and user
configuration but will otherwise be identical for all containers.

## PID1
LXD spawns whatever is located at `/sbin/init` as the initial process of the container (PID 1).
This binary should act as a proper init system, including handling re-parented processes.

LXD's communication with PID1 in the container is limited to two signals:
 - `SIGINT` to trigger a reboot of the container
 - `SIGPWR` (or alternatively `SIGRTMIN`+3) to trigger a clean shutdown of the container

The initial environment of PID1 is blank except for `container=lxc` which can
be used by the init system to detect the runtime.

All file descriptors above the default 3 are closed prior to PID1 being spawned.

## Filesystem
LXD assumes that any image it uses to create a new container from will come with at least:

 - `/dev` (empty)
 - `/proc` (empty)
 - `/sbin/init` (executable)
 - `/sys` (empty)

## Devices
LXD containers have a minimal and ephemeral `/dev` based on a tmpfs filesystem.
Since this is a tmpfs and not a devtmpfs, device nodes will only appear if manually created.

The standard set of device nodes will be setup:

 - `/dev/console`
 - `/dev/fd`
 - `/dev/full`
 - `/dev/log`
 - `/dev/null`
 - `/dev/ptmx`
 - `/dev/random`
 - `/dev/stdin`
 - `/dev/stderr`
 - `/dev/stdout`
 - `/dev/tty`
 - `/dev/urandom`
 - `/dev/zero`

On top of the standard set of devices, the following are also setup for convenience:

 - `/dev/fuse`
 - `/dev/net/tun`
 - `/dev/mqueue`

## Mounts
The following mounts are setup by default under LXD:

 - `/proc` (proc)
 - `/sys` (sysfs)
 - `/sys/fs/cgroup/*` (cgroupfs) (only on kernels lacking cgroup namespace support)

The following paths will also be automatically mounted if present on the host:

 - `/proc/sys/fs/binfmt_misc`
 - `/sys/firmware/efi/efivars`
 - `/sys/fs/fuse/connections`
 - `/sys/fs/pstore`
 - `/sys/kernel/debug`
 - `/sys/kernel/security`

The reason for passing all of those is legacy init systems which require
those to be mounted or be mountabled inside the container.

The majority of those will not be writable (or even readable) from inside an
unprivileged container and will be blocked by our AppArmor policy inside
privileged containers.

## Network
LXD containers may have any number of network devices attached to them.
The naming for those unless overridden by the user is ethX where X is an incrementing number.

## Container to host communication
LXD sets up a socket at `/dev/lxd/sock` which root in the container can use to communicate with LXD on the host.

The API is [documented here](dev-lxd.md).

## LXCFS
If LXCFS is present on the host, it will automatically be setup for the container.

This normally results in a number of `/proc` files being overridden through bind-mounts.
On older kernels a virtual version of `/sys/fs/cgroup` may also be setup by LXCFS.
