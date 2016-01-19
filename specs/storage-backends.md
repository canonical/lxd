# Storage Backends and supported functions
## Feature comparison

LXD supports using plain dirs, Btrfs, LVM, and ZFS for storage of images and containers.  
Where possible, LXD tries to use the advanced features of each system to optimize operations.

Feature                                     | Directory | Btrfs | LVM   | ZFS
:---                                        | :---      | :---  | :---  | :---
Optimized image storage                     | no        | yes   | yes   | yes
Optimized container creation                | no        | yes   | yes   | yes
Optimized snapshot creation                 | no        | yes   | yes   | yes
Optimized image transfer                    | no        | no    | no    | yes
Optimized container transfer                | no        | no    | no    | yes
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

## Notes
### Directory

 - The directory backend is the fallback backend when nothing else is configured or detected.
 - While this backend is fully functional, it's also much slower than
   all the others due to it having to unpack images or do instant copies of
   containers, snapshots and images.

### Btrfs

 - The btrfs backend is automatically used if /var/lib/lxd is on a btrfs filesystem.
 - Uses a subvolume per container, image and snapshot, creating btrfs snapshots when creating a new object.

### LVM

 - LXD uses LVM with thinpool support to offer fast, scalable container and image storage.
 - A LVM VG must be created and then storage.lvm\_vg\_name set to point to it.
 - If a thinpool doesn't already exist, one will be created, the name of the thinpool can be set with storage.lvm\_thinpool\_name .
 - Uses LVs for images, then LV snapshots for containers and container snapshots.
 - The filesystem used for the LVs is ext4.
 - LVs are created with a default size of 100GiB.

### ZFS

 - LXD can use any zpool or part of a zpool. storage.zfs\_pool\_name must be set to the path to be used.
 - ZFS doesn't have (and shouldn't be) mounted on /var/lib/lxd
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
