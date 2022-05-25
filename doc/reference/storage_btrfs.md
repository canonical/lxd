(storage-btrfs)=
# Btrfs

 - Uses a subvolume per instance, image and snapshot, creating Btrfs snapshots when creating a new object.
 - Btrfs can be used as a storage backend inside a container (nesting), so long as the parent container is itself on Btrfs. (But see notes about Btrfs quota via qgroups.)
 - Btrfs supports storage quotas via qgroups. While Btrfs qgroups are
   hierarchical, new subvolumes will not automatically be added to the qgroups
   of their parent subvolumes. This means that users can trivially escape any
   quotas that are set. If adherence to strict quotas is a necessity users
   should be mindful of this and maybe consider using a zfs storage pool with
   refquotas.
 - When using quotas it is critical to take into account that Btrfs extents are immutable so when blocks are
   written they end up in new extents and the old ones remain until all of its data is dereferenced or rewritten.
   This means that a quota can be reached even if the total amount of space used by the current files in the
   subvolume is smaller than the quota. This is seen most often when using VMs on Btrfs due to the random I/O
   nature of using raw disk image files on top of a Btrfs subvolume. Our recommendation is to not use VMs with Btrfs
   storage pools, but if you insist then please ensure that the instance root disk's `size.state` property is set
   to 2x the size of the root disk's size to allow all blocks in the disk image file to be rewritten without
   reaching the qgroup quota. You may also find that using the `btrfs.mount_options=compress-force` storage pool
   option avoids this scenario as a side effect of enabling compression is to reduce the maximum extent size such
   that block rewrites don't cause as much storage to be double tracked. However as this is a storage pool option
   it will affect all volumes on the pool.

## Storage pool configuration
Key                             | Type      | Condition                         | Default                    | Description
:--                             | :---      | :--------                         | :------                    | :----------
btrfs.mount\_options            | string    | Btrfs driver                      | user\_subvol\_rm\_allowed  | Mount options for block devices

## Storage volume configuration
Key                     | Type      | Condition                 | Default                               | Description
:--                     | :---      | :--------                 | :------                               | :----------
security.shifted        | bool      | custom volume             | false                                 | Enable id shifting overlay (allows attach by multiple isolated instances)
security.unmapped       | bool      | custom volume             | false                                 | Disable id mapping for the volume
size                    | string    | appropriate driver        | same as volume.size                   | Size of the storage volume
snapshots.expiry        | string    | custom volume             | -                                     | Controls when snapshots are to be deleted (expects expression like `1M 2H 3d 4w 5m 6y`)
snapshots.pattern       | string    | custom volume             | snap%d                                | Pongo2 template string which represents the snapshot name (used for scheduled snapshots and unnamed snapshots)
snapshots.schedule      | string    | custom volume             | -                                     | Cron expression (`<minute> <hour> <dom> <month> <dow>`), or a comma separated list of schedule aliases `<@hourly> <@daily> <@midnight> <@weekly> <@monthly> <@annually> <@yearly>`

## The following commands can be used to create Btrfs storage pools

 - Create loop-backed pool named "pool1".

```bash
lxc storage create pool1 btrfs
```

 - Create a new pool called "pool1" using an existing Btrfs filesystem at `/some/path`.

```bash
lxc storage create pool1 btrfs source=/some/path
```

 - Create a new pool called "pool1" on `/dev/sdX`.

```bash
lxc storage create pool1 btrfs source=/dev/sdX
```

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
