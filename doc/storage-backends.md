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

With the implementation of the new storage api it is possible to use multiple
storage drivers (e.g. BTRFS and ZFS) at the same time.

## Mixed storage
When switching storage backend after some containers or images already exist, LXD will create any new container  
using the new backend and converting older images to the new backend as needed.

## Non-optimized container transfer
When the filesystem on the source and target hosts differs or when there is no faster way,  
rsync is used to transfer the container content across.

## Notes
### Directory

 - While this backend is fully functional, it's also much slower than
   all the others due to it having to unpack images or do instant copies of
   containers, snapshots and images.

#### The following commands can be used to create directory storage pools

- Create a new directory pool called "pool1".

```
lxc storage create pool1 dir
```

### Btrfs

 - Uses a subvolume per container, image and snapshot, creating btrfs snapshots when creating a new object.
 - When using for nesting, the host btrfs filesystem must be mounted with the "user\_subvol\_rm\_allowed" mount option.

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
lxc storage create pool1 zfs source=/dev/sdX
```



### LVM

 - Uses LVs for images, then LV snapshots for containers and container snapshots.
 - The filesystem used for the LVs is ext4 (can be configured to use xfs instead).

#### The following commands can be used to create LVM storage pools

- Use the existing volume group "my-pool"

```
lxc storage create pool1 lvm source=my-pool
```

- Create new pool named "pool1" on "/dev/sdX".

```
lxc storage create pool1 lvm source=/dev/sdX
```

- Create new pool on "/dev/sdX" with the volume group name "my-pool".

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

- Create a loop-backed pool named "pool1".

```
lxc storage create pool1 zfs
```

- Create a loop-backed pool named "pool1" with the on-disk name "my-tank".

```
lxc storage create pool1 zfs zfs.pool_name=my-tank
```

- Use the existing pool "my-tank".

```
lxc storage create pool1 zfs source=my-tank
```

- Use the existing dataset "my-tank/slice".

```
lxc storage create pool1 zfs source=my-tank/slice
```

- Create a new pool called "pool1" on "/dev/sdX".

```
lxc storage create pool1 zfs source=/dev/sdX
```

- Create a new pool on "/dev/sdX" with the on-disk name "my-tank".

```
lxc storage create pool1 zfs source=/dev/sdX zfs.pool_name=my-tank
```
