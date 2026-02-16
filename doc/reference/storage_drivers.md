---
relatedlinks: "[Benchmarking&#32;LXD&#32;storage&#32;drivers&#32;-&#32;YouTube](https://www.youtube.com/watch?v=z_OKwO5TskA)"
myst:
  html_meta:
    description: Overview of LXD storage drivers, with feature comparison tables for local and non-local drivers and descriptions of their features.
---

(storage-drivers)=
# Storage drivers

LXD supports several storage drivers for storing images, instances, and custom volumes. Where possible, LXD uses the advanced features of each driver to optimize operations.

Storage drivers are divided into local and non-local storage, based on their accessibility.

(storage-drivers-features)=
## Feature comparison

Legend: ✅ supported, ❌ not supported, ➖ not applicable

(storage-drivers-features-local)=
### Local storage features

Feature                                     | Directory | Btrfs | LVM   | ZFS
:---                                        | :---      | :---  | :---  | :---
{ref}`storage-optimized-image-storage`      | ❌        | ✅   | ✅     | ✅
{ref}`storage-optimized-instance-creation`  | ❌        | ✅   | ✅     | ✅
{ref}`storage-optimized-snapshot-creation`  | ❌        | ✅   | ✅     | ✅
{ref}`storage-optimized-backup`             | ❌        | ✅   | ❌     | ✅
{ref}`storage-optimized-volume-transfer`    | ❌        | ✅   | ❌     | ✅
{ref}`storage-optimized-volume-refresh`     | ❌        | ✅   | ✅[^1] | ✅
{ref}`storage-copy-on-write`                | ❌        | ✅   | ✅     | ✅
{ref}`storage-block-based`                  | ❌        | ❌   | ✅     | ❌
{ref}`storage-instant-cloning`              | ❌        | ✅   | ✅     | ✅
{ref}`storage-driver-usable-in-container`   | ✅        | ✅   | ❌     | ✅[^2]
{ref}`storage-restore-older-snapshots`      | ✅        | ✅   | ✅     | ❌
{ref}`storage-quotas`                       | ✅[^3]    | ✅   | ✅     | ✅
{ref}`storage-available-init`               | ✅        | ✅   | ✅     | ✅
{ref}`storage-object-storage`               | ✅        | ✅   | ✅     | ✅
{ref}`storage-volume-recovery`              | ✅        | ✅   | ✅     | ✅

[^1]: Requires {config:option}`storage-lvm-pool-conf:lvm.use_thinpool` to be enabled. Only when refreshing local volumes.
[^2]: Requires {config:option}`storage-zfs-volume-conf:zfs.delegate` to be enabled.
[^3]: % Include content from [storage_dir.md](storage_dir.md)

      ```{include} storage_dir.md
         :start-after: <!-- Include start dir quotas -->
         :end-before: <!-- Include end dir quotas -->
      ```

(storage-drivers-features-nonlocal)=
### Non-local storage features

Feature                                     | Ceph RBD | CephFS | Ceph Object | Dell PowerFlex | Pure Storage | HPE Alletra
:---                                        | :---     | :---   | :---        | :---           | :---         | :---
{ref}`storage-optimized-image-storage`      | ✅       | ➖     | ➖          | ❌              | ✅          | ✅
{ref}`storage-optimized-instance-creation`  | ✅       | ➖     | ➖          | ❌              | ✅          | ✅
{ref}`storage-optimized-snapshot-creation`  | ✅       | ✅     | ➖          | ✅              | ✅          | ✅
{ref}`storage-optimized-backup`             | ❌       | ➖     | ➖          | ❌              | ❌          | ❌
{ref}`storage-optimized-volume-transfer`    | ✅[^4]   | ➖     | ➖          | ❌              | ❌          | ❌
{ref}`storage-optimized-volume-refresh`     | ✅[^5]   | ➖     | ➖          | ❌              | ✅[^6]      | ✅[^6]
{ref}`storage-copy-on-write`                | ✅       | ✅     | ➖          | ✅              | ✅          | ✅
{ref}`storage-block-based`                  | ✅       | ❌     | ➖          | ✅              | ✅          | ✅
{ref}`storage-instant-cloning`              | ✅       | ✅     | ➖          | ❌              | ✅          | ❌
{ref}`storage-driver-usable-in-container`   | ❌       | ➖     | ➖          | ❌              | ❌          | ❌
{ref}`storage-restore-older-snapshots`      | ✅       | ✅     | ➖          | ✅              | ✅          | ✅
{ref}`storage-quotas`                       | ✅       | ✅     | ✅          | ✅              | ✅          | ✅
{ref}`storage-available-init`               | ✅       | ❌     | ❌          | ❌              | ❌          | ❌
{ref}`storage-object-storage`               | ❌       | ❌     | ✅          | ❌              | ❌          | ❌
{ref}`storage-volume-recovery`              | ✅       | ✅     | ✅          | ✅[^7]          | ✅[^7]      | ❌

[^4]: Volumes of type `block` will fall back to non-optimized transfer when migrating to an older LXD server that doesn't yet support the `RBD_AND_RSYNC` migration type.
[^5]: Only for volumes of type `block`.
[^6]: Only when refreshing volumes on the same LXD server using the same storage array.
[^7]: Custom volumes can only be recovered when attached to an instance due to the use of transformed volume names.

For driver-specific information and configuration options, see the pages for the individual drivers, linked below.

(storage-drivers-local)=
## Local

LXD provides drivers for the following types of local storage:

```{toctree}
:maxdepth: 1

storage_dir
storage_btrfs
storage_lvm
storage_zfs
```

A local volume resides on the storage pool of a single LXD server and is only accessible to instances running on that server. In a cluster, other members cannot access local volumes directly.

(storage-drivers-nonlocal)=
## Non-local

LXD supports three categories of non-local storage drivers, described below.

(storage-drivers-remote)=
### Remote

LXD provides drivers for the following types of remote storage:

```{toctree}
:maxdepth: 1

storage_ceph
storage_powerflex
storage_pure
storage_alletra
```

A remote volume is stored on a storage backend that supports cluster-wide access. It is a block volume rather than a shared file system. A remote volume can be attached from any cluster member, but concurrent access by multiple instances or members is not allowed by default and not considered safe. Even when concurrent attachment is allowed (for example, with the volume's `security.shared` option enabled), it can still risk data corruption.

Compared to local storage, remote pools make {ref}`instance migration <howto-instances-migrate>` faster because the instance’s root volume can be re-attached from another cluster member without copying the disk data. With local storage, the root disk must be transferred over the network during migration, which takes more time.

(storage-drivers-shared)=
### Shared

LXD provides the following driver for shared storage:

```{toctree}
:maxdepth: 1

storage_cephfs
```

Like remote volumes, shared volumes are accessible cluster-wide. Unlike remote volumes, shared volumes can be mounted concurrently by multiple instances or cluster members while remaining safe for concurrent access. Shared pools only support custom filesystem volumes; they cannot host instance root volumes or custom block volumes.

(storage-drivers-object)=
### Object storage backend

LXD provides the following driver for an object storage backend:

```{toctree}
:maxdepth: 1

storage_cephobject
```

Ceph Object is a dedicated object storage backend that exposes buckets over HTTP(S). It uses the S3-compatible API and stores data as discrete objects instead of mounted volumes. Like shared storage, using an object storage backend allows concurrent access by multiple instances across the cluster.

(storage-drivers-recommended-setup)=
## Recommended setup

The two best options for use with LXD are ZFS (local) and Ceph (non-local).

Whenever possible, dedicate a full disk or partition to your LXD storage pool. LXD allows you to create loop-based storage, but this isn't recommended for production use. See {ref}`storage-location` for more information.

The {ref}`Directory <storage-dir>` backend should be considered as a last resort option. It supports all main LXD features, but is slow and inefficient because it cannot perform instant copies or snapshots. Therefore, it constantly copies the instance's full storage.

(storage-drivers-security)=
## Security considerations

Currently, the Linux kernel might silently ignore mount options and not apply them when a block-based file system (for example, `ext4`) is already mounted with different mount options.

This means when dedicated disk devices are shared between different storage pools with different mount options set, the second mount might not have the expected mount options.

This becomes security relevant when, for example, one storage pool is supposed to provide `acl` support and the second one is supposed to not provide `acl` support.

For this reason, it is currently recommended to either have dedicated disk devices per storage pool or to ensure that all storage pools that share the same dedicated disk device use the same mount options.

(storage-drivers-features-reference)=
## Features reference

(storage-optimized-image-storage)=
### Optimized image storage

Most LXD storage drivers provide an optimized image storage format. To make instance creation near instantaneous, LXD clones a pre-made image volume when creating an instance rather than unpacking the image tarball from scratch.

To prevent preparing such a volume on a storage pool that might never be used with that image, the volume is generated on demand. Therefore, the first instance takes longer to create than subsequent ones.

(storage-optimized-instance-creation)=
### Optimized instance creation

Some storage drivers can create instances by cloning an existing volume rather than copying all data, which reduces the amount of data that must be written.

(storage-optimized-snapshot-creation)=
### Optimized snapshot creation

Some storage drivers can create snapshots without copying full volumes. This optimizes speed and resources compared to full-copy snapshots.

(storage-optimized-backup)=
### Optimized backup (import/export)

Some storage drivers support LXD’s optimized backup path when exporting and importing instance or volume backups. Optimized exports are usually faster, and snapshots are stored as deltas from the main volume.

(storage-optimized-volume-transfer)=
### Optimized volume transfer

Btrfs, ZFS, and Ceph RBD have an internal send/receive mechanism that allows for optimized volume transfer.

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

(storage-copy-on-write)=
### Copy-on-write

Copy-on-write (CoW) means the storage driver can share unchanged data between a volume and its snapshots. Only changed blocks are written to new locations, which reduces duplication and can improve snapshot performance.

(storage-block-based)=
### Block-based

Block-based storage presents volumes as block devices rather than mounted file systems. If a file system is needed, LXD can format the block volume for containers and custom file system volumes, or the instance can format it (for example, for virtual machines). See {ref}`Pure Storage <storage-pure>`, {ref}`HPE Alletra <storage-alletra>`, and {ref}`Ceph RBD <storage-ceph>` for driver-specific details.

(storage-instant-cloning)=
### Instant cloning

Instant cloning means LXD can quickly create a new volume by cloning an existing one without copying all data.

(storage-driver-usable-in-container)=
### Storage driver usable inside a container

Some storage drivers can be used when LXD itself is running inside a container. Drivers that cannot be used inside a container often need access to host capabilities or devices that containers normally don’t have, and other container limits can also apply.

(storage-restore-older-snapshots)=
### Restore from older snapshots (not latest)

Indicates whether LXD can restore a volume or instance to a snapshot older than the most recent one. Some drivers only allow restoring to the latest snapshot.

(storage-quotas)=
### Storage quotas

Shows whether the storage driver supports enforcing size limits on storage volumes.

(storage-available-init)=
### Available on `lxd init`

Shows whether the storage driver can be selected during `lxd init` (interactive or preseed). Drivers that depend on external storage systems require those systems to be set up first.

(storage-object-storage)=
### Object storage

Object storage provides access to data over HTTP(S). It stores data as discrete objects within buckets, making it ideal for unstructured data such as backups, images, and logs. Unlike volumes, object storage is not mounted to instances but accessed through APIs.

(storage-volume-recovery)=
### Volume recovery

Shows whether {ref}`lxd recover <disaster-recovery>` can re-discover and import existing volumes for the driver after a database loss. Some non-local storage drivers have limitations (see the table footnotes).

## Related topics

{{storage_how}}

{{storage_exp}}
