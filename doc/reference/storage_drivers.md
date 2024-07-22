---
relatedlinks: https://www.youtube.com/watch?v=z_OKwO5TskA
---

(storage-drivers)=
# Storage drivers

LXD supports the following storage drivers for storing images, instances and custom volumes:

```{toctree}
:maxdepth: 1

storage_btrfs
storage_cephfs
storage_cephobject
storage_ceph
storage_powerflex
storage_dir
storage_lvm
storage_zfs
```

See the corresponding pages for driver-specific information and configuration options.

(storage-drivers-features)=
## Feature comparison

Where possible, LXD uses the advanced features of each storage system to optimize operations.

Feature                                     | Directory | Btrfs | LVM   | ZFS    | Ceph RBD | CephFS | Ceph Object | Dell PowerFlex
:---                                        | :---      | :---  | :---  | :---   | :---     | :---   | :---        | :---
{ref}`storage-optimized-image-storage`      | ❌        | ✅   | ✅     | ✅     | ✅       | ➖     | ➖          | ❌
Optimized instance creation                 | ❌        | ✅   | ✅     | ✅     | ✅       | ➖     | ➖          | ❌
Optimized snapshot creation                 | ❌        | ✅   | ✅     | ✅     | ✅       | ✅     | ➖          | ✅
Optimized image transfer                    | ❌        | ✅   | ❌     | ✅     | ✅       | ➖     | ➖          | ❌
Optimized backup (import/export)            | ❌        | ✅   | ❌     | ✅     | ❌       | ➖     | ➖          | ❌
{ref}`storage-optimized-volume-transfer`    | ❌        | ✅   | ❌     | ✅     | ✅[^1]   | ➖     | ➖          | ❌
{ref}`storage-optimized-volume-refresh`     | ❌        | ✅   | ✅[^2] | ✅     | ✅[^3]   | ➖     | ➖          | ❌
Copy on write                               | ❌        | ✅   | ✅     | ✅     | ✅       | ✅     | ➖          | ✅
Block based                                 | ❌        | ❌   | ✅     | ❌      | ✅      | ❌     | ➖          | ✅
Instant cloning                             | ❌        | ✅   | ✅     | ✅     | ✅       | ✅     | ➖          | ❌
Storage driver usable inside a container    | ✅        | ✅   | ❌     | ✅[^4] | ❌       | ➖     | ➖          | ❌
Restore from older snapshots (not latest)   | ✅        | ✅   | ✅     | ❌      | ✅      | ✅     | ➖          | ✅
Storage quotas                              | ✅[^5]    | ✅   | ✅     | ✅     | ✅       | ✅     | ✅          | ✅
Available on `lxd init`                     | ✅        | ✅   | ✅     | ✅     | ✅       | ❌     | ❌          | ❌
Object storage                              | ✅        | ✅   | ✅     | ✅     | ❌       | ❌     | ✅          | ❌

[^1]: Volumes of type `block` will fall back to non-optimized transfer when migrating to an older LXD server that doesn't yet support the `RBD_AND_RSYNC` migration type.
[^2]: Requires {config:option}`storage-lvm-pool-conf:lvm.use_thinpool` to be enabled. Only when refreshing local volumes.
[^3]: Only for volumes of type `block`.
[^4]: Requires {config:option}`storage-zfs-volume-conf:zfs.delegate` to be enabled.
[^5]: % Include content from [storage_dir.md](storage_dir.md)

      ```{include} storage_dir.md
         :start-after: <!-- Include start dir quotas -->
         :end-before: <!-- Include end dir quotas -->
      ```

(storage-optimized-image-storage)=
### Optimized image storage

Most of the storage drivers have some kind of optimized image storage format.
To make instance creation near instantaneous, LXD clones a pre-made image volume when creating an instance rather than unpacking the image tarball from scratch.

To prevent preparing such a volume on a storage pool that might never be used with that image, the volume is generated on demand.
Therefore, the first instance takes longer to create than subsequent ones.

(storage-optimized-volume-transfer)=
### Optimized volume transfer

Btrfs, ZFS and Ceph RBD have an internal send/receive mechanism that allows for optimized volume transfer.

LXD uses this optimized transfer when transferring instances and snapshots between storage pools that use the same storage driver, if the storage driver supports optimized transfer and the optimized transfer is actually quicker.
Otherwise, LXD uses `rsync` to transfer container and file system volumes, or raw block transfer to transfer virtual machine and custom block volumes.

The optimized transfer uses the underlying storage driver's native functionality for transferring data, which is usually faster than using `rsync` or raw block transfer.

(storage-optimized-volume-refresh)=
### Optimized volume refresh

The full potential of the optimized transfer becomes apparent when refreshing a copy of an instance or custom volume that uses periodic snapshots.
If the optimized transfer isn't supported by the driver or its implementation of volume refresh, instead of the delta, the entire volume including its snapshot(s) will be copied using either `rsync` or raw block transfer. LXD will try to keep the overhead low by transferring only the volume itself or any snapshots that are missing on the target.

When optimized refresh is available for an instance or custom volume, LXD bases the refresh on the latest snapshot, which means:

- When you take a first snapshot and refresh the copy, the transfer will take roughly the same time as a full copy.
  LXD transfers the new snapshot and the difference between the snapshot and the main volume.
- For subsequent snapshots, the transfer is considerably faster.
  LXD does not transfer the full new snapshot, but only the difference between the new snapshot and the latest snapshot that already exists on the target.
- When refreshing without a new snapshot, LXD transfers only the differences between the main volume and the latest snapshot on the target.
  This transfer is usually faster than using `rsync` (as long as the latest snapshot is not too outdated).

On the other hand, refreshing copies of instances without snapshots (either because the instance doesn't have any snapshots or because the refresh uses the `--instance-only` flag) would actually be slower than using `rsync` or raw block transfer.
In such cases, the optimized transfer would transfer the difference between the (non-existent) latest snapshot and the main volume, thus the full volume.
Therefore, LXD uses `rsync` or raw block transfer instead of the optimized transfer for refreshes without snapshots.

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

## Related topics

{{storage_how}}

{{storage_exp}}
