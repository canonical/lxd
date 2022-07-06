(storage-btrfs)=
# Btrfs - `btrfs`

{abbr}`Btrfs (B-tree file system)` is a local file system based on the {abbr}`COW (copy-on-write)` principle.
COW means that data is stored to a different block after it has been modified instead of overwriting the existing data, reducing the risk of data corruption.
Unlike other file systems, Btrfs is extent-based, which means that it stores data in contiguous areas of memory.

In addition to basic file system features, Btrfs offers RAID and volume management, pooling, snapshots, checksums, compression and other features.

## Terminology

A Btrfs file system can have *subvolumes*, which are named binary subtrees of the main tree of the file system with their own independent file and directory hierarchy.
A *Btrfs snapshot* is a special type of subvolume that captures a specific state of another subvolume.
Snapshots can be read-write or read-only.

## `btrfs` driver in LXD

The `btrfs` driver in LXD uses a subvolume per instance, image and snapshot.
When creating a new object (for example, launching a new instance), it creates a Btrfs snapshot.

Btrfs can be used as a storage backend inside a container in a nested LXD environment.
In this case, the parent container itself must use Btrfs.
Note, however, that the nested LXD setup does not inherit the Btrfs quotas from the parent (see {ref}`storage-btrfs-quotas` below).

(storage-btrfs-quotas)=
### Quotas

Btrfs supports storage quotas via qgroups.
Btrfs qgroups are hierarchical, but new subvolumes will not automatically be added to the qgroups of their parent subvolumes.
This means that users can trivially escape any quotas that are set.
Therefore, if strict quotas are needed, you should consider using a different storage driver (for example, ZFS with refquotas or LVM with Btrfs on top).

When using quotas, you must take into account that Btrfs extents are immutable.
When blocks are written, they end up in new extents.
The old extents remain until all their data is de-referenced or rewritten.
This means that a quota can be reached even if the total amount of space used by the current files in the subvolume is smaller than the quota.

```{note}
This issue is seen most often when using VMs on Btrfs, due to the random I/O nature of using raw disk image files on top of a Btrfs subvolume.

Therefore, you should never use VMs with Btrfs storage pools.

If you really need to use VMs with Btrfs storage pools, set the instance root disk's {ref}`size.state <instance_device_type_disk>` property to twice the size of the root disk's size.
This configuration allows all blocks in the disk image file to be rewritten without reaching the qgroup quota.
The {ref}`btrfs.mount_options=compress-force <storage-btrfs-pool-config>` storage pool option can also avoid this scenario, because a side effect of enabling compression is to reduce the maximum extent size such that block rewrites don't cause as much storage to be double-tracked.
However, this is a storage pool option, and it therefore affects all volumes on the pool.
```

## Configuration options

The following configuration options are available for storage pools that use the `btrfs` driver and for storage volumes in these pools.

(storage-btrfs-pool-config)=
### Storage pool configuration
Key                             | Type      | Default                    | Description
:--                             | :---      | :------                    | :----------
btrfs.mount\_options            | string    | user\_subvol\_rm\_allowed  | Mount options for block devices
source                          | string    | -                          | Path to block device or loop file or filesystem entry

### Storage volume configuration
Key                     | Type      | Condition                 | Default                               | Description
:--                     | :---      | :--------                 | :------                               | :----------
security.shifted        | bool      | custom volume             | false                                 | Enable id shifting overlay (allows attach by multiple isolated instances)
security.unmapped       | bool      | custom volume             | false                                 | Disable id mapping for the volume
size                    | string    | appropriate driver        | same as volume.size                   | Size of the storage volume
snapshots.expiry        | string    | custom volume             | -                                     | Controls when snapshots are to be deleted (expects expression like `1M 2H 3d 4w 5m 6y`)
snapshots.pattern       | string    | custom volume             | snap%d                                | Pongo2 template string which represents the snapshot name (used for scheduled snapshots and unnamed snapshots)
snapshots.schedule      | string    | custom volume             | -                                     | {{snapshot_schedule_format}}

## Growing a loop backed Btrfs pool
LXD doesn't let you directly grow a loop backed Btrfs pool, but you can do so with:

```bash
sudo truncate -s +5G /var/lib/lxd/disks/<POOL>.img
sudo losetup -c <LOOPDEV>
sudo btrfs filesystem resize max /var/lib/lxd/storage-pools/<POOL>/
```

(NOTE: For users of the snap, use `/var/snap/lxd/common/mntns/var/snap/lxd/common/lxd/` instead of `/var/lib/lxd/`)
- LOOPDEV refers to the mounted loop device (e.g. `/dev/loop8`) associated with the storage pool image.
- The mounted loop devices can be found using the following command:
```bash
losetup -l
```
