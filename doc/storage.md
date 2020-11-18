# Storage configuration
LXD supports creating and managing storage pools and storage volumes.
General keys are top-level. Driver specific keys are namespaced by driver name.
Volume keys apply to any volume created in the pool unless the value is
overridden on a per-volume basis.

## Storage pool configuration
Key                             | Type      | Condition                         | Default                    | API Extension                      | Description
:--                             | :---      | :--------                         | :------                    | :------------                      | :----------
size                            | string    | appropriate driver and source     | 0                          | storage                            | Size of the storage pool in bytes (suffixes supported). (Currently valid for loop based pools and zfs.)
source                          | string    | -                                 | -                          | storage                            | Path to block device or loop file or filesystem entry
btrfs.mount\_options            | string    | btrfs driver                      | user\_subvol\_rm\_allowed  | storage\_btrfs\_mount\_options     | Mount options for block devices
ceph.cluster\_name              | string    | ceph driver                       | ceph                       | storage\_driver\_ceph              | Name of the ceph cluster in which to create new storage pools.
ceph.osd.force\_reuse           | bool      | ceph driver                       | false                      | storage\_ceph\_force\_osd\_reuse   | Force using an osd storage pool that is already in use by another LXD instance.
ceph.osd.pg\_num                | string    | ceph driver                       | 32                         | storage\_driver\_ceph              | Number of placement groups for the osd storage pool.
ceph.osd.pool\_name             | string    | ceph driver                       | name of the pool           | storage\_driver\_ceph              | Name of the osd storage pool.
ceph.osd.data\_pool\_name       | string    | ceph driver                       | -                          | storage\_driver\_ceph              | Name of the osd data pool.
ceph.rbd.clone\_copy            | string    | ceph driver                       | true                       | storage\_driver\_ceph              | Whether to use RBD lightweight clones rather than full dataset copies.
ceph.user.name                  | string    | ceph driver                       | admin                      | storage\_ceph\_user\_name          | The ceph user to use when creating storage pools and volumes.
cephfs.cluster\_name            | string    | cephfs driver                     | ceph                       | storage\_driver\_cephfs            | Name of the ceph cluster in which to create new storage pools.
cephfs.path                     | string    | cephfs driver                     | /                          | storage\_driver\_cephfs            | The base path for the CEPHFS mount
cephfs.user.name                | string    | cephfs driver                     | admin                      | storage\_driver\_cephfs            | The ceph user to use when creating storage pools and volumes.
lvm.thinpool\_name              | string    | lvm driver                        | LXDThinPool                | storage                            | Thin pool where volumes are created.
lvm.use\_thinpool               | bool      | lvm driver                        | true                       | storage\_lvm\_use\_thinpool        | Whether the storage pool uses a thinpool for logical volumes.
lvm.vg\_name                    | string    | lvm driver                        | name of the pool           | storage                            | Name of the volume group to create.
lvm.vg.force\_reuse             | bool      | lvm driver                        | false                      | storage\_lvm\_vg\_force\_reuse     | Force using an existing non-empty volume group.
volume.lvm.stripes              | string    | lvm driver                        | -                          | storage\_lvm\_stripes              | Number of stripes to use for new volumes (or thin pool volume).
volume.lvm.stripes.size         | string    | lvm driver                        | -                          | storage\_lvm\_stripes              | Size of stripes to use (at least 4096 bytes and multiple of 512bytes).
rsync.bwlimit                   | string    | -                                 | 0 (no limit)               | storage\_rsync\_bwlimit            | Specifies the upper limit to be placed on the socket I/O whenever rsync has to be used to transfer storage entities.
rsync.compression               | bool      | appropriate driver                | true                       | storage\_rsync\_compression        | Whether to use compression while migrating storage pools.
volatile.initial\_source        | string    | -                                 | -                          | storage\_volatile\_initial\_source | Records the actual source passed during creating (e.g. /dev/sdb).
volatile.pool.pristine          | string    | -                                 | true                       | storage\_driver\_ceph              | Whether the pool has been empty on creation time.
volume.block.filesystem         | string    | block based driver (lvm)          | ext4                       | storage                            | Filesystem to use for new volumes
volume.block.mount\_options     | string    | block based driver (lvm)          | discard                    | storage                            | Mount options for block devices
volume.size                     | string    | appropriate driver                | unlimited (10GB for block) | storage                            | Default volume size
volume.zfs.remove\_snapshots    | bool      | zfs driver                        | false                      | storage                            | Remove snapshots as needed
volume.zfs.use\_refquota        | bool      | zfs driver                        | false                      | storage                            | Use refquota instead of quota for space.
zfs.clone\_copy                 | string    | zfs driver                        | true                       | storage\_zfs\_clone\_copy          | Whether to use ZFS lightweight clones rather than full dataset copies (boolean) or "rebase" to copy based on the initial image.
zfs.pool\_name                  | string    | zfs driver                        | name of the pool           | storage                            | Name of the zpool

Storage pool configuration keys can be set using the lxc tool with:

```bash
lxc storage set [<remote>:]<pool> <key> <value>
```

## Storage volume configuration
Key                     | Type      | Condition                 | Default                               | API Extension                    | Description
:--                     | :---      | :--------                 | :------                               | :------------                    | :----------
size                    | string    | appropriate driver        | same as volume.size                   | storage                          | Size of the storage volume
block.filesystem        | string    | block based driver        | same as volume.block.filesystem       | storage                          | Filesystem of the storage volume
block.mount\_options    | string    | block based driver        | same as volume.block.mount\_options   | storage                          | Mount options for block devices
security.shifted        | bool      | custom volume             | false                                 | storage\_shifted                 | Enable id shifting overlay (allows attach by multiple isolated instances)
security.unmapped       | bool      | custom volume             | false                                 | storage\_unmapped                | Disable id mapping for the volume
lvm.stripes             | string    | lvm driver                | -                                     | storage\_lvm\_stripes            | Number of stripes to use for new volumes (or thin pool volume).
lvm.stripes.size        | string    | lvm driver                | -                                     | storage\_lvm\_stripes            | Size of stripes to use (at least 4096 bytes and multiple of 512bytes).
snapshots.expiry        | string    | custom volume             | -                                     | custom\_volume\_snapshot\_expiry | Controls when snapshots are to be deleted (expects expression like `1M 2H 3d 4w 5m 6y`)
snapshots.schedule      | string    | custom volume             | -                                     | volume\_snapshot\_scheduling     | Cron expression (`<minute> <hour> <dom> <month> <dow>`)
snapshots.pattern       | string    | custom volume             | snap%d                                | volume\_snapshot\_scheduling     | Pongo2 template string which represents the snapshot name (used for scheduled snapshots and unnamed snapshots)
zfs.remove\_snapshots   | string    | zfs driver                | same as volume.zfs.remove\_snapshots  | storage                          | Remove snapshots as needed
zfs.use\_refquota       | string    | zfs driver                | same as volume.zfs.zfs\_requota       | storage                          | Use refquota instead of quota for space

Storage volume configuration keys can be set using the lxc tool with:

```bash
lxc storage volume set [<remote>:]<pool> <volume> <key> <value>
```

## Storage volume content types
Storage volumes can be either `filesystem` or `block` type.

Containers and container images are always going to be using `filesystem`.
Virtual machines and virtual machine images are always going to be using `block`.

Custom storage volumes can be either types with the default being `filesystem`.
Those custom storage volumes of type `block` can only be attached to virtual machines.

Block custom storage volumes can be created with:

```bash
lxc storage volume create [<remote>]:<pool> <name> --type=block
```

# Where to store LXD data
Depending on the storage backends used, LXD can either share the filesystem with its host or keep its data separate.

## Sharing with the host
This is usually the most space efficient way to run LXD and possibly the easiest to manage.
It can be done with:

 - `dir` backend on any backing filesystem
 - `btrfs` backend if the host is btrfs and you point LXD to a dedicated subvolume
 - `zfs` backend if the host is zfs and you point LXD to a dedicated dataset on your zpool

## Dedicated disk/partition
In this mode, LXD's storage will be completely independent from the host.
This can be done by having LXD use an empty partition on your main disk or by having it use a full dedicated disk.

This is supported by all storage drivers except `dir`, `ceph` and `cephfs`.

## Loop disk
If neither of the options above are possible for you, LXD can create a loop file
on your main drive and then have the selected storage driver use that.

This is functionally similar to using a disk/partition but uses a large file on your main drive instead.
This comes at a performance penalty as every writes need to go through the storage driver and then your main
drive's filesystem. The loop files also usually cannot be shrunk.
They will grow up to the limit you select but deleting instances or images will not cause the file to shrink.

# Storage Backends and supported functions
## Feature comparison
LXD supports using ZFS, btrfs, LVM or just plain directories for storage of images, instances and custom volumes.  
Where possible, LXD tries to use the advanced features of each system to optimize operations.

Feature                                     | Directory | Btrfs | LVM   | ZFS  | CEPH
:---                                        | :---      | :---  | :---  | :--- | :---
Optimized image storage                     | no        | yes   | yes   | yes  | yes
Optimized instance creation                 | no        | yes   | yes   | yes  | yes
Optimized snapshot creation                 | no        | yes   | yes   | yes  | yes
Optimized image transfer                    | no        | yes   | no    | yes  | yes
Optimized instance transfer                 | no        | yes   | no    | yes  | yes
Copy on write                               | no        | yes   | yes   | yes  | yes
Block based                                 | no        | no    | yes   | no   | yes
Instant cloning                             | no        | yes   | yes   | yes  | yes
Storage driver usable inside a container    | yes       | yes   | no    | no   | no
Restore from older snapshots (not latest)   | yes       | yes   | yes   | no   | yes
Storage quotas                              | yes(\*)   | yes   | yes   | yes  | no

## Recommended setup
The two best options for use with LXD are ZFS and btrfs.  
They have about similar functionalities but ZFS is more reliable if available on your particular platform.

Whenever possible, you should dedicate a full disk or partition to your LXD storage pool.  
While LXD will let you create loop based storage, this isn't recommended for production use.

Similarly, the directory backend is to be considered as a last resort option.  
It does support all main LXD features, but is terribly slow and inefficient as it can't perform  
instant copies or snapshots and so needs to copy the entirety of the instance's storage every time.

## Security Considerations

Currently, the Linux Kernel may not apply mount options and silently ignore
them when a block-based filesystem (e.g. `ext4`) is already mounted with
different options. This means when dedicated disk devices are shared between
different storage pools with different mount options set, the second mount may
not have the expected mount options. This becomes security relevant, when e.g.
one storage pool is supposed to provide `acl` support and the second one is
supposed to not provide `acl` support. For this reason it is currently
recommended to either have dedicated disk devices per storage pool or ensure
that all storage pools that share the same dedicated disk device use the same
mount options.

## Optimized image storage
All backends but the directory backend have some kind of optimized image storage format.  
This is used by LXD to make instance creation near instantaneous by simply cloning a pre-made  
image volume rather than unpack the image tarball from scratch.

As it would be wasteful to prepare such a volume on a storage pool that may never be used with that image,  
the volume is generated on demand, causing the first instance to take longer to create than subsequent ones.

## Optimized instance transfer
ZFS, btrfs and CEPH RBD have an internal send/receive mechanisms which allow for optimized volume transfer.  
LXD uses those features to transfer instances and snapshots between servers.

When such capabilities aren't available, either because the storage driver doesn't support it  
or because the storage backend of the source and target servers differ,  
LXD will fallback to using rsync to transfer the individual files instead.

When rsync has to be used LXD allows to specify an upper limit on the amount of
socket I/O by setting the `rsync.bwlimit` storage pool property to a non-zero
value.

## Default storage pool
There is no concept of a default storage pool in LXD.  
Instead, the pool to use for the instance's root is treated as just another "disk" device in LXD.

The device entry looks like:

```yaml
  root:
    type: disk
    path: /
    pool: default
```

And it can be directly set on an instance ("-s" option to "lxc launch" and "lxc init")  
or it can be set through LXD profiles.

That latter option is what the default LXD setup (through "lxd init") will do for you.  
The same can be done manually against any profile using (for the "default" profile):

```bash
lxc profile device add default root disk path=/ pool=default
```

## I/O limits
I/O limits in IOp/s or MB/s can be set on storage devices when attached to an
instance (see [Instances](instances.md)).

Those are applied through the Linux `blkio` cgroup controller which makes it possible  
to restrict I/O at the disk level (but nothing finer grained than that).

Because those apply to a whole physical disk rather than a partition or path, the following restrictions apply:

 - Limits will not apply to filesystems that are backed by virtual devices (e.g. device mapper).
 - If a filesystem is backed by multiple block devices, each device will get the same limit.
 - If the instance is passed two disk devices that are each backed by the same disk,  
   the limits of the two devices will be averaged.

It's also worth noting that all I/O limits only apply to actual block device access,  
so you will need to consider the filesystem's own overhead when setting limits.  
This also means that access to cached data will not be affected by the limit.

## Notes and examples
### Directory

 - While this backend is fully functional, it's also much slower than
   all the others due to it having to unpack images or do instant copies of
   instances, snapshots and images.
 - Quotas are supported with the directory backend when running on
   either ext4 or XFS with project quotas enabled at the filesystem level.

#### The following commands can be used to create directory storage pools

 - Create a new directory pool called "pool1".

```bash
lxc storage create pool1 dir
```

 - Use an existing directory for "pool2".

```bash
lxc storage create pool2 dir source=/data/lxd
```

### CEPH

- Uses RBD images for images, then snapshots and clones to create instances
  and snapshots.
- Due to the way copy-on-write works in RBD, parent filesystems can't be
  removed until all children are gone. As a result, LXD will automatically
  prefix any removed but still referenced object with "zombie_" and keep it
  until such time the references are gone and it can safely be removed.
- Note that LXD will assume it has full control over the osd storage pool.
  It is recommended to not maintain any non-LXD owned filesystem entities in
  a LXD OSD storage pool since LXD might delete them.
- Note that sharing the same osd storage pool between multiple LXD instances is
  not supported. LXD only allows sharing of an OSD storage pool between
  multiple LXD instances only for backup purposes of existing instances via
  `lxd import`. In line with this, LXD requires the "ceph.osd.force_reuse"
  property to be set to true. If not set, LXD will refuse to reuse an osd
  storage pool it detected as being in use by another LXD instance.
- When setting up a ceph cluster that LXD is going to use we recommend using
  `xfs` as the underlying filesystem for the storage entities that are used to
  hold OSD storage pools. Using `ext4` as the underlying filesystem for the
  storage entities is not recommended by Ceph upstream. You may see unexpected
  and erratic failures which are unrelated to LXD itself.

#### The following commands can be used to create Ceph storage pools

- Create a osd storage pool named "pool1" in the CEPH cluster "ceph".

```bash
lxc storage create pool1 ceph
```

- Create a osd storage pool named "pool1" in the CEPH cluster "my-cluster".

```bash
lxc storage create pool1 ceph ceph.cluster_name=my-cluster
```

- Create a osd storage pool named "pool1" with the on-disk name "my-osd".

```bash
lxc storage create pool1 ceph ceph.osd.pool_name=my-osd
```

- Use the existing osd storage pool "my-already-existing-osd".

```bash
lxc storage create pool1 ceph source=my-already-existing-osd
```

### CEPHFS

 - Can only be used for custom storage volumes
 - Supports snapshots if enabled on the server side

### Btrfs

 - Uses a subvolume per instance, image and snapshot, creating btrfs snapshots when creating a new object.
 - btrfs can be used as a storage backend inside a container (nesting), so long as the parent container is itself on btrfs. (But see notes about btrfs quota via qgroups.)
 - btrfs supports storage quotas via qgroups. While btrfs qgroups are
   hierarchical, new subvolumes will not automatically be added to the qgroups
   of their parent subvolumes. This means that users can trivially escape any
   quotas that are set. If adherence to strict quotas is a necessity users
   should be mindful of this and maybe consider using a zfs storage pool with
   refquotas.

#### The following commands can be used to create BTRFS storage pools

 - Create loop-backed pool named "pool1".

```bash
lxc storage create pool1 btrfs
```

 - Create a new pool called "pool1" using an existing btrfs filesystem at `/some/path`.

```bash
lxc storage create pool1 btrfs source=/some/path
```

 - Create a new pool called "pool1" on `/dev/sdX`.

```bash
lxc storage create pool1 btrfs source=/dev/sdX
```

#### Growing a loop backed btrfs pool
LXD doesn't let you directly grow a loop backed btrfs pool, but you can do so with:

```bash
sudo truncate -s +5G /var/lib/lxd/disks/<POOL>.img
sudo losetup -c <LOOPDEV>
sudo btrfs filesystem resize max /var/lib/lxd/storage-pools/<POOL>/
```

(NOTE: For users of the snap, use `/var/snap/lxd/common/lxd/ instead of /var/lib/lxd/`)

### LVM

 - Uses LVs for images, then LV snapshots for instances and instance snapshots.
 - The filesystem used for the LVs is ext4 (can be configured to use xfs instead).
 - By default, all LVM storage pools use an LVM thinpool in which logical
   volumes for all LXD storage entities (images, instances, etc.) are created.
   This behavior can be changed by setting "lvm.use\_thinpool" to "false". In
   this case, LXD will use normal logical volumes for all non-instance
   snapshot storage entities (images, instances, etc.). This means most storage
   operations will need to fallback to rsyncing since non-thinpool logical
   volumes do not support snapshots of snapshots. Note that this entails
   serious performance impacts for the LVM driver causing it to be close to the
   fallback DIR driver both in speed and storage usage. This option should only
   be chosen if the use-case renders it necessary.
 - For environments with high instance turn over (e.g continuous integration)
   it may be important to tweak the archival `retain_min` and `retain_days`
   settings in `/etc/lvm/lvm.conf` to avoid slowdowns when interacting with
   LXD.

#### The following commands can be used to create LVM storage pools

 - Create a loop-backed pool named "pool1". The LVM Volume Group will also be called "pool1".

```bash
lxc storage create pool1 lvm
```

 - Use the existing LVM Volume Group called "my-pool"

```bash
lxc storage create pool1 lvm source=my-pool
```

 - Use the existing LVM Thinpool called "my-pool" in Volume Group "my-vg".

```bash
lxc storage create pool1 lvm source=my-vg lvm.thinpool_name=my-pool
```

 - Create a new pool named "pool1" on `/dev/sdX`. The LVM Volume Group will also be called "pool1".

```bash
lxc storage create pool1 lvm source=/dev/sdX
```

 - Create a new pool called "pool1" using `/dev/sdX` with the LVM Volume Group called "my-pool".

```bash
lxc storage create pool1 lvm source=/dev/sdX lvm.vg_name=my-pool
```

### ZFS

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

   Also note that instance copies use ZFS snapshots, so you also cannot
   restore an instance to a snapshot taken before the last copy without
   having to also delete instance copies.

   Copying the wanted snapshot into a new instance and then deleting
   the old instance does however work, at the cost of losing any other
   snapshot the instance may have had.

 - Note that LXD will assume it has full control over the ZFS pool or dataset.
   It is recommended to not maintain any non-LXD owned filesystem entities in
   a LXD zfs pool or dataset since LXD might delete them.
 - When quotas are used on a ZFS dataset LXD will set the ZFS "quota" property.
   In order to have LXD set the ZFS "refquota" property, either set
   "zfs.use\_refquota" to "true" for the given dataset or set
   "volume.zfs.use\_refquota" to true on the storage pool. The former option
   will make LXD use refquota only for the given storage volume the latter will
   make LXD use refquota for all storage volumes in the storage pool.
 - I/O quotas (IOps/MBs) are unlikely to affect ZFS filesystems very
   much. That's because of ZFS being a port of a Solaris module (using SPL)
   and not a native Linux filesystem using the Linux VFS API which is where
   I/O limits are applied.

#### The following commands can be used to create ZFS storage pools

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
#### Growing a loop backed ZFS pool
LXD doesn't let you directly grow a loop backed ZFS pool, but you can do so with:

```bash
sudo truncate -s +5G /var/lib/lxd/disks/<POOL>.img
sudo zpool set autoexpand=on lxd
sudo zpool online -e lxd /var/lib/lxd/disks/<POOL>.img
sudo zpool set autoexpand=off lxd
```

(NOTE: For users of the snap, use `/var/snap/lxd/common/lxd/ instead of /var/lib/lxd/`)

#### Enabling TRIM on existing pools
LXD will automatically enable trimming support on all newly created pools on ZFS 0.8 or later.

This helps with the lifetime of SSDs by allowing better block re-use by the controller.
This also will allow freeing space on the root filesystem when using a loop backed ZFS pool.

For systems which were upgraded from pre-0.8 to 0.8, this can be enabled with a one time action of:

 - zpool upgrade ZPOOL-NAME
 - zpool set autotrim=on ZPOOL-NAME
 - zpool trim ZPOOL-NAME

This will make sure that TRIM is automatically issued in the future as
well as cause TRIM on all currently unused space.
