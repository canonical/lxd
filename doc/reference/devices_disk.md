(devices-disk)=
# Type: `disk`

```{note}
The `disk` device type is supported for both containers and VMs.
It supports hotplugging for both containers and VMs.
```

Disk devices supply additional storage to instances.
For containers, they are essentially mount points inside the instance (either as a bind-mount of an existing file or directory on the host, or, if the source is a block device, a regular mount).
Virtual machines share host-side mounts or directories through `9p` or `virtiofs` (if available), or as VirtIO disks for block-based disks.

Disk devices can also be created by {ref}`attaching a storage volume to an instance <storage-attach-volume>`.

LXD supports the following additional source types:

Ceph RBD
: Mount an existing Ceph RBD device that is externally managed.

  LXD can use Ceph to manage an internal file system for the instance, but if you have an existing Ceph RBD that you would like to use for an instance, you can add it with the following command:

      lxc config device add <instance_name> <device_name> disk source=ceph:<pool_name>/<volume_name> ceph.user_name=<user_name> ceph.cluster_name=<cluster_name> path=<path_in_instance>

CephFS
: Mount an existing CephFS device that is externally managed.

  LXD can use Ceph to manage an internal file system for the instance, but if you have an existing Ceph file system that you would like to use for an instance, you can add it with the following command:

      lxc config device add <instance_name> <device_name> disk source=cephfs:<fs_name>/<path> ceph.user_name=<user_name> ceph.cluster_name=<cluster_name> path=<path_in_instance>

VM `cloud-init`
: Generate a `cloud-init` configuration ISO from the `cloud-init.vendor-data`, `cloud-init.user-data` and `user.meta-data` configuration keys (see {ref}`instance-options`) and attach it to the VM, so that `cloud-init` running inside the VM detects the drive on boot and applies the configuration.

  This source type is only applicable to VMs.

  To add such a device, use the following command:

      lxc config device add <instance_name> <device_name> disk source=cloud-init:config

## Device options

`disk` devices have the following device options:

Key                 | Type      | Default   | Required  | Description
:--                 | :--       | :--       | :--       | :--
`boot.priority`     | integer   | -         | no        | Boot priority for VMs (higher value boots first)
`ceph.cluster_name` | string    | `ceph`    | no        | The cluster name of the Ceph cluster (required for Ceph or CephFS sources)
`ceph.user_name`    | string    | `admin`   | no        | The user name of the Ceph cluster (required for Ceph or CephFS sources)
`limits.max`        | string    | -         | no        | I/O limit in byte/s or IOPS for both read and write (same as setting both `limits.read` and `limits.write`)
`limits.read`       | string    | -         | no        | I/O limit in byte/s (various suffixes supported, see {ref}`instances-limit-units`) or in IOPS (must be suffixed with `iops`) - see also {ref}`storage-configure-IO`
`limits.write`      | string    | -         | no        | I/O limit in byte/s (various suffixes supported, see {ref}`instances-limit-units`) or in IOPS (must be suffixed with `iops`) - see also {ref}`storage-configure-IO`
`path`              | string    | -         | yes       | Path inside the instance where the disk will be mounted (only for containers)
`pool`              | string    | -         | no        | The storage pool to which the disk device belongs (only applicable for storage volumes managed by LXD)
`propagation`       | string    | -         | no        | Controls how a bind-mount is shared between the instance and the host (can be one of `private`, the default, or `shared`, `slave`, `unbindable`,  `rshared`, `rslave`, `runbindable`,  `rprivate`; see the Linux Kernel [shared subtree](https://www.kernel.org/doc/Documentation/filesystems/sharedsubtree.txt) documentation for a full explanation) <!-- wokeignore:rule=slave -->
`raw.mount.options` | string    | -         | no        | File system specific mount options
`readonly`          | bool      | `false`   | no        | Controls whether to make the mount read-only
`recursive`         | bool      | `false`   | no        | Controls whether to recursively mount the source path
`required`          | bool      | `true`    | no        | Controls whether to fail if the source doesn't exist
`shift`             | bool      | `false`   | no        | Sets up a shifting overlay to translate the source UID/GID to match the instance (only for containers)
`size`              | string    | -         | no        | Disk size in bytes (various suffixes supported, see {ref}`instances-limit-units`) - only supported for the `rootfs` (`/`)
`size.state`        | string    | -         | no        | Same as `size`, but applies to the file-system volume used for saving runtime state in VMs
`source`            | string    | -         | yes       | Path on the host, either to a file/directory or to a block device
