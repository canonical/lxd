(devices-disk)=
# Type: `disk`

Supported instance types: container, VM

Disk entries are essentially mount points inside the instance. They can
either be a bind-mount of an existing file or directory on the host, or
if the source is a block device, a regular mount.

They can also be created by {ref}`attaching a storage volume to an instance <storage-attach-volume>`.

LXD supports the following additional source types:

- Ceph RBD: Mount from existing Ceph RBD device that is externally managed. LXD can use Ceph to manage an internal file system for the instance, but in the event that a user has a previously existing Ceph RBD that they would like use for this instance, they can use this command.

  Example command:

  ```
  lxc config device add <instance> ceph-rbd1 disk source=ceph:<my_pool>/<my-volume> ceph.user_name=<username> ceph.cluster_name=<username> path=/ceph
  ```

- CephFS: Mount from existing CephFS device that is externally managed. LXD can use Ceph to manage an internal file system for the instance, but in the event that a user has a previously existing Ceph file system that they would like use for this instance, they can use this command.

  Example command:

  ```
  lxc config device add <instance> ceph-fs1 disk source=cephfs:<my-fs>/<some-path> ceph.user_name=<username> ceph.cluster_name=<username> path=/cephfs
  ```

- VM cloud-init: Generate a cloud-init configuration ISO from the `user.vendor-data`, `user.user-data` and `user.meta-data` configuration keys and attach to the VM so that cloud-init running inside the VM guest will detect the drive on boot and apply the configuration. Only applicable to virtual-machine instances.

  Example command:

  ```
  lxc config device add <instance> config disk source=cloud-init:config
  ```

The following properties exist:

Key                 | Type      | Default   | Required  | Description
:--                 | :--       | :--       | :--       | :--
`limits.read`       | string    | -         | no        | I/O limit in byte/s (various suffixes supported, see {ref}`instances-limit-units`) or in IOPS (must be suffixed with `iops`) - see also {ref}`storage-configure-IO`
`limits.write`      | string    | -         | no        | I/O limit in byte/s (various suffixes supported, see {ref}`instances-limit-units`) or in IOPS (must be suffixed with `iops`) - see also {ref}`storage-configure-IO`
`limits.max`        | string    | -         | no        | Same as modifying both `limits.read` and `limits.write`
`path`              | string    | -         | yes       | Path inside the instance where the disk will be mounted (only for containers).
`source`            | string    | -         | yes       | Path on the host, either to a file/directory or to a block device
`required`          | bool      | `true`    | no        | Controls whether to fail if the source doesn't exist
`readonly`          | bool      | `false`   | no        | Controls whether to make the mount read-only
`size`              | string    | -         | no        | Disk size in bytes (various suffixes supported, see {ref}`instances-limit-units`). This is only supported for the `rootfs` (`/`).
`size.state`        | string    | -         | no        | Same as size above but applies to the file-system volume used for saving runtime state in virtual machines.
`recursive`         | bool      | `false`   | no        | Whether or not to recursively mount the source path
`pool`              | string    | -         | no        | The storage pool the disk device belongs to. This is only applicable for storage volumes managed by LXD
`propagation`       | string    | -         | no        | Controls how a bind-mount is shared between the instance and the host. (Can be one of `private`, the default, or `shared`, `slave`, `unbindable`,  `rshared`, `rslave`, `runbindable`,  `rprivate`. Please see the Linux Kernel [shared subtree](https://www.kernel.org/doc/Documentation/filesystems/sharedsubtree.txt) documentation for a full explanation) <!-- wokeignore:rule=slave -->
`shift`             | bool      | `false`   | no        | Set up a shifting overlay to translate the source UID/GID to match the instance (only for containers)
`raw.mount.options` | string    | -         | no        | File system specific mount options
`ceph.user_name`    | string    | `admin`   | no        | If source is Ceph or CephFS then Ceph `user_name` must be specified by user for proper mount
`ceph.cluster_name` | string    | `ceph`    | no        | If source is Ceph or CephFS then Ceph `cluster_name` must be specified by user for proper mount
`boot.priority`     | integer   | -         | no        | Boot priority for VMs (higher boots first)
