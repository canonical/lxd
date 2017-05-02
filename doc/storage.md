# Storage configuration
LXD supports creating and managing storage pools and storage volumes.
General keys are top-level. Driver specific keys are namespaced by driver name.
Volume keys apply to any volume created in the pool unless the value is
overridden on a per-volume basis.

## Storage pool configuration

Key                             | Type      | Condition                         | Default                 | Description
:--                             | :--       | :--                               | :--                     | :--
size                            | string    | appropriate driver and source     | 0                       | Size of the storage pool in bytes (suffixes supported). (Currently valid for loop based pools and zfs.)
source                          | string    | -                                 | -                       | Path to block device or loop file or filesystem entry
btrfs.mount\_options            | string    | btrfs driver                      | user_subvol_rm_allowed  | Mount options for block devices
lvm.thinpool\_name              | string    | lvm driver                        | LXDPool                 | Thin pool where images and containers are created.
lvm.use\_thinpool               | bool      | lvm driver                        | true                    | Whether the storage pool uses a thinpool for logical volumes.
lvm.vg\_name                    | string    | lvm driver                        | name of the pool        | Name of the volume group to create.
rsync.bwlimit                   | string    | -                                 | 0 (no limit)            | Specifies the upper limit to be placed on the socket I/O whenever rsync has to be used to transfer storage entities.
volume.block.filesystem         | string    | block based driver (lvm)          | ext4                    | Filesystem to use for new volumes
volume.block.mount\_options     | string    | block based driver (lvm)          | discard                 | Mount options for block devices
volume.size                     | string    | appropriate driver                | 0                       | Default volume size
volume.zfs.remove\_snapshots    | bool      | zfs driver                        | false                   | Remove snapshots as needed
volume.zfs.use\_refquota        | bool      | zfs driver                        | false                   | Use refquota instead of quota for space.
zfs.clone\_copy                 | bool      | zfs driver                        | true                    | Whether to use ZFS lightweight clones rather than full dataset copies.
zfs.pool\_name                  | string    | zfs driver                        | name of the pool        | Name of the zpool

Storage pool configuration keys can be set using the lxc tool with:

    lxc storage set [<remote>:]<pool> <key> <value>

## Storage volume configuration

Key                     | Type      | Condition                 | Default                               | Description
:--                     | :--       | :--                       | :--                                   | :--
size                    | string    | appropriate driver        | same as volume.size                   | Size of the storage volume
block.filesystem        | string    | block based driver (lvm)  | same as volume.block.filesystem       | Filesystem of the storage volume
block.mount\_options    | string    | block based driver (lvm)  | same as volume.block.mount\_options   | Mount options for block devices
zfs.remove\_snapshots   | string    | zfs driver                | same as volume.zfs.remove\_snapshots  | Remove snapshots as needed
zfs.use\_refquota       | string    | zfs driver                | same as volume.zfs.zfs\_requota       | Use refquota instead of quota for space.

Storage volume configuration keys can be set using the lxc tool with:

    lxc storage volume set [<remote>:]<pool> <volume> <key> <value>

# Storage Backends and supported functions
## Feature comparison
LXD supports using ZFS, btrfs, LVM or just plain directories for storage of images and containers.  
Where possible, LXD tries to use the advanced features of each system to optimize operations.

Feature                                     | Directory | Btrfs | LVM   | ZFS
:---                                        | :---      | :---  | :---  | :---
Optimized image storage                     | no        | yes   | yes   | yes
Optimized container creation                | no        | yes   | yes   | yes
Optimized snapshot creation                 | no        | yes   | yes   | yes
Optimized image transfer                    | no        | yes   | no    | yes
Optimized container transfer                | no        | yes   | no    | yes
Copy on write                               | no        | yes   | yes   | yes
Block based                                 | no        | no    | yes   | no
Instant cloning                             | no        | yes   | yes   | yes
Storage driver usable inside a container    | yes       | yes   | no    | no
Restore from older snapshots (not latest)   | yes       | yes   | yes   | no
Storage quotas                              | no        | yes   | no    | yes

## Recommended setup
The two best options for use with LXD are ZFS and btrfs.  
They have about similar functionalities but ZFS is more reliable if available on your particular platform.

Whenever possible, you should dedicate a full disk or partition to your LXD storage pool.  
While LXD will let you create loop based storage, this isn't a recommended for production use.

Similarly, the directory backend is to be considered as a last resort option.  
It does support all main LXD features, but is terribly slow and inefficient as it can't perform  
instant copies or snapshots and so needs to copy the entirety of the container's filesystem every time.

## Optimized image storage
All backends but the directory backend have some kind of optimized image storage format.  
This is used by LXD to make container creation near instantaneous by simply cloning a pre-made  
image volume rather than unpack the image tarball from scratch.

As it would be wasteful to prepare such a volume on a storage pool that may never be used with that image,  
the volume is generated on demand, causing the first container to take longer to create than subsequent ones.

## Optimized container transfer
ZFS and btrfs both have an internal send/receive mechanism which allows for optimized volume transfer.  
LXD uses those features to transfer containers and snapshots between servers.

When such capabilities aren't available, either because the storage driver doesn't support it  
or because the storage backend of the source and target servers differ,  
LXD will fallback to using rsync to transfer the individual files instead.

When rsync has to be used LXD allows to specify an upper limit on the amount of
socket I/O by setting the "rsync.bwlimit" storage pool property to a non-zero
value.

## Default storage pool
There is no concept of a default storage pool in LXD.  
Instead, the pool to use for the container's root is treated as just another "disk" device in LXD.

The device entry looks like:
```
  root:
    type: disk
    path: /
    pool: default
```

And it can be directly set on a container ("-s" option to "lxc launch" and "lxc init")  
or it can be set through LXD profiles.

That latter option is what the default LXD setup (through "lxd init") will do for you.  
The same can be done manually against any profile using (for the "default" profile):
```
lxc profile device add default root disk path=/ pool=default
```

## Notes and examples
### Directory

 - While this backend is fully functional, it's also much slower than
   all the others due to it having to unpack images or do instant copies of
   containers, snapshots and images.

#### The following commands can be used to create directory storage pools

 - Create a new directory pool called "pool1".

```
lxc storage create pool1 dir
```

 - Use an existing directory for "pool2".

```
lxc storage create pool2 dir source=/data/lxd
```

### Btrfs

 - Uses a subvolume per container, image and snapshot, creating btrfs snapshots when creating a new object.
 - btrfs can be used as a storage backend inside a container (nesting), so long as the parent container is itself on btrfs. (But see notes about btrfs quota via qgroups.)
 - btrfs supports storage quotas via qgroups. While btrfs qgroups are
   hierarchical, new subvolumes will not automatically be added to the qgroups
   of their parent subvolumes. This means that users can trivially escape any
   quotas that are set. If adherence to strict quotas is a necessity users
   should be mindful of this and maybe consider using a zfs storage pool with
   refquotas.

#### The following commands can be used to create BTRFS storage pools

 - Create loop-backed pool named "pool1".

```
lxc storage create pool1 btrfs
```

 - Create a btrfs subvolume named "pool1" on the btrfs filesystem "/some/path" and use as pool.

```
lxc storage create pool1 btrfs source=/some/path
```

 - Create a new pool called "pool1" on "/dev/sdX".

```
lxc storage create pool1 btrfs source=/dev/sdX
```

### LVM

 - Uses LVs for images, then LV snapshots for containers and container snapshots.
 - The filesystem used for the LVs is ext4 (can be configured to use xfs instead).
 - By default, all LVM storage pools use an LVM thinpool in which logical
   volumes for all LXD storage entities (images, containers, etc.) are created.
   This behavior can be changed by setting "lvm.use_thinpool" to "false". In
   this case, LXD will use normal logical volumes for all non-container
   snapshot storage entities (images, containers etc.). This means most storage
   operations will need to fallback to rsyncing since non-thinpool logical
   volumes do not support snapshots of snapshots. Note that this entails
   serious performance impacts for the LVM driver causing it to be close to the
   fallback DIR driver both in speed and storage usage. This option should only
   be chosen if the use-case renders it necessary.

#### The following commands can be used to create LVM storage pools

 - Create a loop-backed pool named "pool1". The LVM Volume Group will also be called "pool1".

```
lxc storage create pool1 lvm
```

 - Use the existing LVM Volume Group called "my-pool"

```
lxc storage create pool1 lvm source=my-pool
```

 - Create a new pool named "pool1" on "/dev/sdX". The LVM Volume Group will also be called "pool1".

```
lxc storage create pool1 lvm source=/dev/sdX
```

 - Create a new pool called "pool1" using "/dev/sdX" with the LVM Volume Group called "my-pool".

```
lxc storage create pool1 lvm source=/dev/sdX lvm.vg_name=my-pool
```

### ZFS

 - Uses ZFS filesystems for images, then snapshots and clones to create containers and snapshots.
 - Due to the way copy-on-write works in ZFS, parent filesystems can't
   be removed until all children are gone. As a result, LXD will
   automatically rename any removed but still referenced object to a random
   deleted/ path and keep it until such time the references are gone and it
   can safely be removed.
 - ZFS as it is today doesn't support delegating part of a pool to a
   container user. Upstream is actively working on this.
 - ZFS doesn't support restoring from snapshots other than the latest
   one. You can however create new containers from older snapshots which
   makes it possible to confirm the snapshots is indeed what you want to
   restore before you remove the newer snapshots.

   Also note that container copies use ZFS snapshots, so you also cannot
   restore a container to a snapshot taken before the last copy without
   having to also delete container copies.

   Copying the wanted snapshot into a new container and then deleting
   the old container does however work, at the cost of losing any other
   snapshot the container may have had.
 - Note that LXD will assume it has full control over the zfs pool or dataset.
   It is recommended to not maintain any non-LXD owned filesystem entities in
   a LXD zfs pool or dataset since LXD might delete them.

#### The following commands can be used to create ZFS storage pools

 - Create a loop-backed pool named "pool1". The ZFS Zpool will also be called "pool1".

```
lxc storage create pool1 zfs
```

 - Create a loop-backed pool named "pool1" with the ZFS Zpool called "my-tank".

```
lxc storage create pool1 zfs zfs.pool\_name=my-tank
```

 - Use the existing ZFS Zpool "my-tank".

```
lxc storage create pool1 zfs source=my-tank
```

 - Use the existing ZFS dataset "my-tank/slice".

```
lxc storage create pool1 zfs source=my-tank/slice
```

 - Create a new pool called "pool1" on "/dev/sdX". The ZFS Zpool will also be called "pool1".

```
lxc storage create pool1 zfs source=/dev/sdX
```

 - Create a new pool on "/dev/sdX" with the ZFS Zpool called "my-tank".

```
lxc storage create pool1 zfs source=/dev/sdX zfs.pool_name=my-tank
```

#### Growing a loop backed ZFS pool
LXD doesn't let you directly grow a loop backed ZFS pool, but you can do so with:

```
sudo truncate -s +5G /var/lib/lxd/disks/<POOL>.img
sudo zpool set autoexpand=on lxd
sudo zpool online -e lxd /var/lib/lxd/disks/<POOL>.img
sudo zpool set autoexpand=off lxd
```
