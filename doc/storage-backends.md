# Storage Backends and supported functions
## Feature comparison

LXD supports using plain dirs, Btrfs, LVM, and ZFS for storage of images and containers.  
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
Nesting support                             | yes       | yes   | no    | no
Restore from older snapshots (not latest)   | yes       | yes   | yes   | no
Storage quotas                              | no        | yes   | no    | yes

## Mixed storage
When switching storage backend after some containers or images already exist, LXD will create any new container  
using the new backend and converting older images to the new backend as needed.

## Non-optimized container transfer
When the filesystem on the source and target hosts differs or when there is no faster way,  
rsync is used to transfer the container content across.

## I/O limits
I/O limits in IOp/s or MB/s can be set on storage devices when attached to a container (see [Containers](containers.md)).

Those are applied through the Linux `blkio` cgroup controller which makes it possible  
to restrict I/O at the disk level (but nothing finer grained than that).

Because those apply to a whole physical disk rather than a partition or path, the following restrictions apply:

 - Limits will not apply to filesystems that are backed by virtual devices (e.g. device mapper).
 - If a fileystem is backed by multiple block devices, each device will get the same limit.
 - If the container is passed two disk devices that are each backed by the same disk,  
   the limits of the two devices will be averaged.

It's also worth noting that all I/O limits only apply to actual block device access,  
so you will need to consider the filesystem's own overhead when setting limits.  
This also means that access to cached data will not be affected by the limit.

## Notes
### Directory

 - The directory backend is the fallback backend when nothing else is configured or detected.
 - While this backend is fully functional, it's also much slower than
   all the others due to it having to unpack images or do instant copies of
   containers, snapshots and images.

### Btrfs

 - The btrfs backend is automatically used if /var/lib/lxd is on a btrfs filesystem.
 - Uses a subvolume per container, image and snapshot, creating btrfs snapshots when creating a new object.
 - When using for nesting, the host btrfs filesystem must be mounted with the `user_subvol_rm_allowed` mount option.
 - btrfs supports storage quotas via qgroups. While btrfs qgroups are
   hierarchical, new subvolumes will not automatically be added to the qgroups
   of their parent subvolumes. This means that users can trivially escape any
   quotas that are set. If adherence to strict quotas is a necessity users
   should be mindful of this and maybe consider using a zfs storage pool with
   refquotas.

### LVM

 - A LVM VG must be created and then `storage.lvm_vg_name` set to point to it.
 - If a thinpool doesn't already exist, one will be created, the name of the thinpool can be set with `storage.lvm_thinpool_name` .
 - Uses LVs for images, then LV snapshots for containers and container snapshots.
 - The filesystem used for the LVs is ext4 (can be configured to use xfs instead).
 - LVs are created with a default size of 10GiB (can be configured through).

### ZFS

 - LXD can use any zpool or part of a zpool. `storage.zfs_pool_name` must be set to the path to be used.
 - ZFS doesn't have to (and shouldn't be) mounted on `/var/lib/lxd`
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
 - I/O quotas (IOps/MBs) are unlikely to affect ZFS filesystems very
   much. That's because of ZFS being a port of a Solaris module (using SPL)
   and not a native Linux filesystem using the Linux VFS API which is where
   I/O limits are applied.

#### Growing a loop backed ZFS pool
LXD doesn't let you directly grow a loop backed ZFS pool, but you can do so with:

```bash
sudo truncate -s +5G /var/lib/lxd/zfs.img
sudo zpool set autoexpand=on lxd
sudo zpool online -e lxd /var/lib/lxd/zfs.img
sudo zpool set autoexpand=off lxd
```
