---
relatedlinks: https://www.youtube.com/watch?v=z_OKwO5TskA
---

(storage-drivers)=
# Storage drivers

LXD supports the following storage drivers for storing images, instances and custom volumes:

```{toctree}
:maxdepth: 1

storage_dir
storage_btrfs
storage_lvm
storage_zfs
storage_ceph
storage_cephfs
storage_cephobject
```
See the corresponding pages for driver-specific information and configuration options.

(storage-drivers-features)=
## Feature comparison

Where possible, LXD uses the advanced features of each storage system to optimize operations.

Feature                                     | Directory | Btrfs | LVM   | ZFS  | Ceph RBD | CephFS | Ceph Object
:---                                        | :---      | :---  | :---  | :--- | :---     | :---   | :---
{ref}`storage-optimized-image-storage`      | no        | yes   | yes   | yes  | yes      | n/a    | n/a
Optimized instance creation                 | no        | yes   | yes   | yes  | yes      | n/a    | n/a
Optimized snapshot creation                 | no        | yes   | yes   | yes  | yes      | yes    | n/a
Optimized image transfer                    | no        | yes   | no    | yes  | yes      | n/a    | n/a
{ref}`storage-optimized-instance-transfer`  | no        | yes   | no    | yes  | yes      | n/a    | n/a
Copy on write                               | no        | yes   | yes   | yes  | yes      | yes    | n/a
Block based                                 | no        | no    | yes   | no   | yes      | no     | n/a
Instant cloning                             | no        | yes   | yes   | yes  | yes      | yes    | n/a
Storage driver usable inside a container    | yes       | yes   | no    | no   | no       | n/a    | n/a
Restore from older snapshots (not latest)   | yes       | yes   | yes   | no   | yes      | yes    | n/a
Storage quotas                              | yes<sup>{ref}`* <storage-dir-quotas>`</sup>| yes   | yes   | yes  | yes  | yes    | yes
Available on `lxd init`                     | yes       | yes   | yes   | yes  | yes      | no     | no
Object storage                              | no        | no    | no    | no   | no       | no     | yes

(storage-optimized-image-storage)=
### Optimized image storage

All storage drivers except for the directory driver have some kind of optimized image storage format.
To make instance creation near instantaneous, LXD clones a pre-made image volume when creating an instance rather than unpacking the image tarball from scratch.

To prevent preparing such a volume on a storage pool that might never be used with that image, the volume is generated on demand.
Therefore, the first instance takes longer to create than subsequent ones.

(storage-optimized-instance-transfer)=
### Optimized instance transfer

Btrfs, ZFS and Ceph RBD have an internal send/receive mechanism that allows for optimized volume transfer.
LXD uses this mechanism to transfer instances and snapshots between servers.

This optimized transfer is available only when transferring volumes between storage pools that use the same storage driver.
When transferring between storage pools that use different drivers or drivers that don't support optimized instance transfer, LXD uses `rsync` to transfer the individual files instead.

When using `rsync`, you can specify an upper limit on the amount of socket I/O by setting the `rsync.bwlimit` storage pool property to a non-zero value.

## Recommended setup

The two best options for use with LXD are ZFS and Btrfs.
They have similar functionalities, but ZFS is more reliable.

Whenever possible, you should dedicate a full disk or partition to your LXD storage pool.
LXD allows to create loop-based storage, but this isn't recommended for production use.
See {ref}`storage-location` for more information.

The directory backend should be considered as a last resort option.
It supports all main LXD features, but is slow and inefficient because it cannot perform instant copies or snapshots.
Therefore, it constantly copies the instance's full storage.

## Security considerations

Currently, the Linux kernel might silently ignore mount options and not apply them when a block-based file system (for example, `ext4`) is already mounted with different mount options.
This means when dedicated disk devices are shared between different storage pools with different mount options set, the second mount might not have the expected mount options.
This becomes security relevant when, for example, one storage pool is supposed to provide `acl` support and the second one is supposed to not provide `acl` support.

For this reason, it is currently recommended to either have dedicated disk devices per storage pool or to ensure that all storage pools that share the same dedicated disk device use the same mount options.
