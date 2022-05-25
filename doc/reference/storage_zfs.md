(storage-zfs)=
# ZFS

 - When LXD creates a ZFS pool, compression is enabled by default.
 - Uses ZFS filesystems for images, then snapshots and clones to create instances and snapshots.
 - Due to the way copy-on-write works in ZFS, parent filesystems can't
   be removed until all children are gone. As a result, LXD will
   automatically rename any removed but still referenced object to a random
   deleted/ path and keep it until such time the references are gone and it
   can safely be removed.
 - ZFS as it is today doesn't support delegating part of a pool to a
   container user. Upstream is actively working on this.
 - ZFS doesn't support restoring from snapshots other than the latest
   one. You can however create new instances from older snapshots which
   makes it possible to confirm the snapshots is indeed what you want to
   restore before you remove the newer snapshots.

   LXD can be configured to automatically discard the newer snapshots during restore.
   This can be configured through the `volume.zfs.remove_snapshots` pool option.

   However note that instance copies use ZFS snapshots too, so you also cannot
   restore an instance to a snapshot taken before the last copy without having
   to also delete all its descendants.

   Copying the wanted snapshot into a new instance and then deleting
   the old instance does however work, at the cost of losing any other
   snapshot the instance may have had.

 - Note that LXD will assume it has full control over the ZFS pool or dataset.
   It is recommended to not maintain any non-LXD owned filesystem entities in
   a LXD ZFS pool or dataset since LXD might delete them.
 - When quotas are used on a ZFS dataset LXD will set the ZFS "quota" property.
   In order to have LXD set the ZFS "refquota" property, either set
   "zfs.use\_refquota" to "true" for the given dataset or set
   "volume.zfs.use\_refquota" to true on the storage pool. The former option
   will make LXD use refquota only for the given storage volume the latter will
   make LXD use refquota for all storage volumes in the storage pool. Also you can
   set "zfs.reserve\_space" on the volume or "volume.zfs.reserve\_space" on the
   storage pool to use ZFS "reservation"/"refreservation" along with
   "quota"/"refquota".
 - I/O quotas (IOps/MBs) are unlikely to affect ZFS filesystems very
   much. That's because of ZFS being a port of a Solaris module (using SPL)
   and not a native Linux filesystem using the Linux VFS API which is where
   I/O limits are applied.

## Storage pool configuration
Key                           | Type                          | Default                                 | Description
:--                           | :---                          | :------                                 | :----------
size                          | string                        | 0                                       | Size of the storage pool in bytes (suffixes supported). (Currently valid for loop based pools and ZFS.)
source                        | string                        | -                                       | Path to block device or loop file or filesystem entry
zfs.clone\_copy               | string                        | true                                    | Whether to use ZFS lightweight clones rather than full dataset copies (boolean) or "rebase" to copy based on the initial image
zfs.export                    | bool                          | true                                    | Disable zpool export while unmount performed
zfs.pool\_name                | string                        | name of the pool                        | Name of the zpool

## Storage volume configuration
Key                     | Type      | Condition                 | Default                               | Description
:--                     | :---      | :--------                 | :------                               | :----------
security.shifted        | bool      | custom volume             | false                                 | Enable id shifting overlay (allows attach by multiple isolated instances)
security.unmapped       | bool      | custom volume             | false                                 | Disable id mapping for the volume
size                    | string    | appropriate driver        | same as volume.size                   | Size of the storage volume
snapshots.expiry        | string    | custom volume             | -                                     | Controls when snapshots are to be deleted (expects expression like `1M 2H 3d 4w 5m 6y`)
snapshots.pattern       | string    | custom volume             | snap%d                                | Pongo2 template string which represents the snapshot name (used for scheduled snapshots and unnamed snapshots)
snapshots.schedule      | string    | custom volume             | -                                     | Cron expression (`<minute> <hour> <dom> <month> <dow>`), or a comma separated list of schedule aliases `<@hourly> <@daily> <@midnight> <@weekly> <@monthly> <@annually> <@yearly>`
zfs.blocksize           | string    | ZFS driver                | same as volume.zfs.blocksize          | Size of the ZFS block in range from 512 to 16MiB (must be power of 2). For block volume maximum value of 128KiB will be used even though higher value is set
zfs.remove\_snapshots   | string    | ZFS driver                | same as volume.zfs.remove\_snapshots  | Remove snapshots as needed
zfs.use\_refquota       | string    | ZFS driver                | same as volume.zfs.zfs\_refquota      | Use refquota instead of quota for space
zfs.reserve\_space      | string    | ZFS driver                | false                                 | Use reservation/refreservation along with qouta/refquota

## The following commands can be used to create ZFS storage pools

 - Create a loop-backed pool named "pool1". The ZFS Zpool will also be called "pool1".

```bash
lxc storage create pool1 zfs
```

 - Create a loop-backed pool named "pool1" with the ZFS Zpool called "my-tank".

```bash
lxc storage create pool1 zfs zfs.pool_name=my-tank
```

 - Use the existing ZFS Zpool "my-tank".

```bash
lxc storage create pool1 zfs source=my-tank
```

 - Use the existing ZFS dataset "my-tank/slice".

```bash
lxc storage create pool1 zfs source=my-tank/slice
```

 - Create a new pool called "pool1" on `/dev/sdX`. The ZFS Zpool will also be called "pool1".

```bash
lxc storage create pool1 zfs source=/dev/sdX
```

 - Create a new pool on `/dev/sdX` with the ZFS Zpool called "my-tank".

```bash
lxc storage create pool1 zfs source=/dev/sdX zfs.pool_name=my-tank
```
## Growing a loop backed ZFS pool
LXD doesn't let you directly grow a loop backed ZFS pool, but you can do so with:

```bash
sudo truncate -s +5G /var/lib/lxd/disks/<POOL>.img
sudo zpool set autoexpand=on lxd
sudo zpool online -e lxd /var/lib/lxd/disks/<POOL>.img
sudo zpool set autoexpand=off lxd
```

(NOTE: For users of the snap, use `/var/snap/lxd/common/lxd/` instead of `/var/lib/lxd/`)

## Enabling TRIM on existing pools
LXD will automatically enable trimming support on all newly created pools on ZFS 0.8 or later.

This helps with the lifetime of SSDs by allowing better block re-use by the controller.
This also will allow freeing space on the root filesystem when using a loop backed ZFS pool.

For systems which were upgraded from pre-0.8 to 0.8, this can be enabled with a one time action of:

 - zpool upgrade ZPOOL-NAME
 - zpool set autotrim=on ZPOOL-NAME
 - zpool trim ZPOOL-NAME

This will make sure that TRIM is automatically issued in the future as
well as cause TRIM on all currently unused space.
