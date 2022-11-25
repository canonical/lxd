
# ZFS - `zfs`

```{youtube} https://www.youtube.com/watch?v=ysLi_LYAs_M
```

`ZFS (Zettabyte file system)` combines the roles of a file system and volume manager.
A ZFS installation can span across multiple storage devices and is very scalable, allowing you to add disks to expand the available space in the storage pool immediately. It allows for multiple levels of redundancy of physical storage devices.

ZFS is a block-based file system that protects against data corruption by using checksums to verify, confirm and correct every operation.
To run at a sufficient speed, this mechanism requires a powerful environment with a lot of RAM.

In addition, ZFS offers snapshots and replication, RAID management, copy-on-write clones, compression and other features.

To use ZFS on Ubuntu, make sure you have `zfsutils-linux` installed on your machine. Other distributions might use a different naming convention.

## Terminology

ZFS creates logical units based on physical storage devices. These logical units are called *VDEVs* (virtual devices). VDEVs are made up of one or more physical disks, partitions, or even loop-mounted files; basically any block device. While a single disk can be used as a VDEV, most typically VDEVs are either 2 or more disks in a mirror configuration, 3 or more disks in the ZFS equivalent of a RAID5 (*raid-z1*), 4 or more disks configured as RAID6 (*raid-z2*), and so on. While the configuration of any particular VDEV is quite flexible, once created, VDEVs are immutable and cannot be changed (other than to replace a failed disk).
Sets of one or more VDEVs are then combined into what are called *ZFS pools* or *zpools*. Zpools are the fundamental unit of storage in ZFS, and while VDEVs are iummutable, one can always add additional VDEVs to a zpool when necessary. Data written to a zpool is striped across all VDEVs in the pool, which is why VDEVs are rarely configured as a single disk (since losing one disk then results in a loss of the entire pool). Think of a zpool as a pool of storage where one creates one or more *datasets*. Unless quotas ae applied, these datasets can grow or shrink as necessary so long at the total space used by all datasets in a zpool is less than the size of the zpool itself. All data in a zpool is associated with one or more datasets.

These `datasets` can be of different types:

- A *`ZFS filesystem`* is equivalent to a partition or a mounted file system.
- A *ZFS volume* represents a block device.
- A *ZFS snapshot* captures a specific state of either a `ZFS filesystem` or a ZFS volume.
  ZFS snapshots are read-only.
- A *ZFS clone* is a writable copy of a ZFS snapshot.

## `zfs` driver in LXD

The `zfs` driver in LXD uses `ZFS filesystems` and ZFS volumes for images and custom storage volumes, and ZFS snapshots and clones to create instances from images and for instance and custom volume snapshots.
By default, LXD enables compression when creating a ZFS pool.

LXD assumes that it has full control over the ZFS pool and all `datasets` contained therein.
You should never do manual operations on an LXD managed ZFS storage pool, nor should you ever maintain any `datasets` or file system entities that are not owned by LXD in such a pool because LXD might delete them. 

Due to the way copy-on-write works in ZFS, parent `ZFS filesystems` can't be removed until all children are gone. For example, when you create multiple containers from the same OS image, by default LXD makes a snapshot of this image and then creates these containers as clones of the snapshot. In the parlance of ZFS, the containers are *children of both the snapshot and the OS image*. (See [`zfs.clone_copy`](storage-zfs-pool-config) below for how to alter this behavior.)

As a result, LXD automatically renames any objects that are removed but still referenced.
Such objects are kept at a random `deleted/` path until all references are gone and the object can safely be removed.
Note that this method might have ramifications for restoring snapshots.
See {ref}`storage-zfs-limitations` below.

LXD automatically enables trimming support on all newly created pools on ZFS 0.8 or later.
This increases the lifetime of SSDs by allowing better block re-use by the controller, and it also allows to free space on the root file system when using a loop-backed ZFS pool.
If you are running a ZFS version earlier than 0.8 and want to enable trimming, upgrade to at least version 0.8.
Then use the following commands to make sure that trimming is automatically enabled for the ZFS pool in the future and trim all currently unused space:

    zpool upgrade ZPOOL-NAME
    zpool set autotrim=on ZPOOL-NAME
    zpool trim ZPOOL-NAME


### Limitations

The `zfs` driver has the following limitations:

Delegating part of a pool
: ZFS doesn't support delegating part of a pool to a container user.
  Upstream is actively working on providing this functionality.

Restoring from older snapshots
: ZFS doesn't support restoring from snapshots other than the latest one.
  You can, however, create new instances from older snapshots.
  This method makes it possible to confirm whether a specific snapshot contains what you need.
  After determining the correct snapshot, you can `remove the newer snapshots <storage-edit-snapshots>` so that the snapshot you need is the latest one and you can restore it.

  Alternatively, you can configure LXD to automatically discard the newer snapshots during restore.
  To do so, set the [`zfs.remove_snapshots`](storage-zfs-vol-config) configuration for the volume (or the corresponding `volume.zfs.remove_snapshots` configuration on the storage pool for all volumes in the pool).

  Note, however, that if [`zfs.clone_copy`](storage-zfs-pool-config) is set to `true`, instance copies use ZFS snapshots too.
  In that case, you cannot restore an instance to a snapshot taken before the last copy without having to also delete all its descendants.
  If this is not an option, you can copy the wanted snapshot into a new instance and then delete the old instance.
  You will, however, lose any other snapshots the instance might have had.

Observing I/O quotas
: I/O quotas are unlikely to affect `ZFS filesystems` very much.
  That's because ZFS is a port of a Solaris module (using SPL) and not a native Linux file system using the Linux VFS API, which is where I/O limits are applied.

### Quotas

ZFS provides two different quota properties: `quota` and `refquota`.
`quota` restricts the total size of a `dataset`, including its snapshots and clones.
`refquota` restricts only the size of the data in the `dataset`, not its snapshots and clones.

By default, LXD uses the `quota` property when you set up a quota for your storage volume.
If you want to use the `refquota` property instead, set the [`zfs.use_refquota`](storage-zfs-vol-config) configuration for the volume (or the corresponding `volume.zfs.use_refquota` configuration on the storage pool for all volumes in the pool).

You can also set the [`zfs.use_reserve_space`](storage-zfs-vol-config) (or `volume.zfs.use_reserve_space`) configuration to use ZFS `reservation` or `refreservation` along with `quota` or `refquota`.

## Configuration options

The following configuration options are available for storage pools that use the `zfs` driver and for storage volumes in these pools.


### Storage pool configuration

Key                           | Type                          | Default                                 | Description
:--                           | :---                          | :------                                 | :----------
`size`                        | string                        | auto (20% of free disk space, >= 5 GiB and <= 30 GiB) | Size of the storage pool when creating loop-based pools (in bytes, suffixes supported)
`source`                      | string                        | -                                       | Path to an existing block device, loop file or ZFS dataset/pool
`zfs.clone_copy`              | string                        | `true`                                  | Whether to use ZFS lightweight clones rather than full {spellexception}`dataset` copies (Boolean), or `rebase` to copy based on the initial image
`zfs.export`                  | bool                          | `true`                                  | Disable zpool export while unmount performed
`zfs.pool_name`               | string                        | name of the pool                        | Name of the zpool

{{volume_configuration}}


### Storage volume configuration

Key                     | Type      | Condition                 | Default                                        | Description
:--                     | :---      | :--------                 | :------                                        | :----------
`security.shifted`      | bool      | custom volume             | same as `volume.security.shifted` or `false`   | {{enable_ID_shifting}}
`security.unmapped`     | bool      | custom volume             | same as `volume.security.unmapped` or `false`  | Disable ID mapping for the volume
`size`                  | string    | appropriate driver        | same as `volume.size`                          | Size/quota of the storage volume
`snapshots.expiry`      | string    | custom volume             | same as `volume.snapshots.expiry`              | {{snapshot_expiry_format}}
`snapshots.pattern`     | string    | custom volume             | same as `volume.snapshots.pattern` or `snap%d` | {{snapshot_pattern_format}}
`snapshots.schedule`    | string    | custom volume             | same as `snapshots.schedule`                   | {{snapshot_schedule_format}}
`zfs.blocksize`         | string    | ZFS driver                | same as `volume.zfs.blocksize`                 | Size of the ZFS block in range from 512 to 16 MiB (must be power of 2) - for block volume, a maximum value of 128 KiB will be used even if a higher value is set
`zfs.remove_snapshots`  | bool      | ZFS driver                | same as `volume.zfs.remove_snapshots` or `false` | Remove snapshots as needed
`zfs.use_refquota`      | bool      | ZFS driver                | same as `volume.zfs.use_refquota` or `false`   | Use `refquota` instead of `quota` for space
`zfs.reserve_space`     | bool      | ZFS driver                | same as `volume.zfs.reserve_space` or `false`  | Use `reservation`/`refreservation` along with `quota`/`refquota`

### Storage bucket configuration

To enable storage buckets for local storage pool drivers and allow applications to access the buckets via the S3 protocol, you must configure the `core.storage_buckets_address` server setting (see {ref}`server`).

Key                     | Type      | Condition                 | Default                                        | Description
:--                     | :---      | :--------                 | :------                                        | :----------
`size`                  | string    | appropriate driver        | same as `volume.size`                          | Size/quota of the storage bucket
