# Storage drivers

```{toctree}
:maxdepth: 1

storage_dir
storage_ceph
storage_cephfs
storage_btrfs
storage_lvm
storage_zfs
```

## Feature comparison
LXD supports using ZFS, Btrfs, LVM or just plain directories for storage of images, instances and custom volumes.
Where possible, LXD tries to use the advanced features of each system to optimize operations.

Feature                                     | Directory | Btrfs | LVM   | ZFS  | Ceph | CephFS
:---                                        | :---      | :---  | :---  | :--- | :--- | :---
Optimized image storage                     | no        | yes   | yes   | yes  | yes  | n/a
Optimized instance creation                 | no        | yes   | yes   | yes  | yes  | n/a
Optimized snapshot creation                 | no        | yes   | yes   | yes  | yes  | yes
Optimized image transfer                    | no        | yes   | no    | yes  | yes  | n/a
Optimized instance transfer                 | no        | yes   | no    | yes  | yes  | n/a
Copy on write                               | no        | yes   | yes   | yes  | yes  | yes
Block based                                 | no        | no    | yes   | no   | yes  | no
Instant cloning                             | no        | yes   | yes   | yes  | yes  | yes
Storage driver usable inside a container    | yes       | yes   | no    | no   | no   | n/a
Restore from older snapshots (not latest)   | yes       | yes   | yes   | no   | yes  | yes
Storage quotas                              | yes(\*)   | yes   | yes   | yes  | yes  | yes

## Recommended setup
The two best options for use with LXD are ZFS and Btrfs.
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
ZFS, Btrfs and Ceph RBD have an internal send/receive mechanisms which allow for optimized volume transfer.
LXD uses those features to transfer instances and snapshots between servers.

When such capabilities aren't available, either because the storage driver doesn't support it
or because the storage backend of the source and target servers differ,
LXD will fallback to using rsync to transfer the individual files instead.

When rsync has to be used LXD allows to specify an upper limit on the amount of
socket I/O by setting the `rsync.bwlimit` storage pool property to a non-zero
value.

## I/O limits
I/O limits in IOp/s or MB/s can be set on storage devices when attached to an
instance (see [Instances](/instances.md)).

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
