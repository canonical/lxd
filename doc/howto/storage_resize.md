---
discourse: 1333
---

# How to resize storage

If you need more storage, you can increase the size of your storage pool or your storage volume.
In some cases, it is also possible to reduce the size of a storage volume.

(storage-resize-grow-pool)=
## Grow a storage pool

To increase the size of a storage pool, follow these general steps:

1. Grow the size of the storage on disk.
1. Let the file system know of the size change.

See the specific commands for different storage drivers below.

````{tabs}

```{group-tab} Btrfs

Enter the following commands to grow a loop-backed Btrfs pool by 5 Gigabytes:

    sudo truncate -s +5G <LXD_lib_dir>/disks/<pool_name>.img
    sudo losetup -c <loop_device>
    sudo btrfs filesystem resize max <LXD_lib_dir>/storage-pools/<pool_name>/

Replace the following variables:

`<LXD_lib_dir>`
: `/var/snap/lxd/common/mntns/var/snap/lxd/common/lxd/` if you are using the snap, or `/var/lib/lxd/` otherwise.

`<pool_name>`
: The name of your storage pool (for example, `my-pool`).

`<loop_device>`
: The mounted loop device that is associated with the storage pool image (for example, `/dev/loop8`).
  To find your loop device, enter `losetup -j <LXD_lib_dir>/disks/<pool_name>.img`.
  You can also use `losetup -l` to list all mounted loop devices.
```
```{group-tab} LVM

Enter the following commands to grow a loop-backed LVM pool by 5 Gigabytes:

    sudo truncate -s +5G <LXD_lib_dir>/disks/<pool_name>.img
    sudo losetup -c <loop_device>
    sudo pvresize <loop_device>

For LVM thin pools, you must then expand the `LXDThinPool` logical volume in your pool (skip this step if you are not using a thin pool):

    sudo lvextend <pool_name>/LXDThinPool -l+100%FREE

Replace the following variables:

`<LXD_lib_dir>`
: `/var/snap/lxd/common/lxd/` if you are using the snap, or `/var/lib/lxd/` otherwise.

`<pool_name>`
: The name of your storage pool (for example, `my-pool`).

`<loop_device>`
: The mounted loop device that is associated with the storage pool image (for example, `/dev/loop8`).
  To find your loop device, enter `losetup -j <LXD_lib_dir>/disks/<pool_name>.img`.
  You can also use `losetup -l` to list all mounted loop devices.

You can check that the pool was resized as expected with the following commands:

    sudo pvs <loop_device> # Check the size of the physical volume
    sudo vgs <pool_name> # Check the size of the volume group
    sudo lvs <pool_name>/LXDThinPool # Thin pool only: check the size of the thin-pool logical volume
```
```{group-tab} ZFS

Enter the following commands to grow a loop-backed ZFS pool by 5 Gigabytes:

    sudo truncate -s +5G <LXD_lib_dir>/disks/<pool_name>.img
    sudo zpool set autoexpand=on <pool_name>
    sudo zpool online -e <pool_name> <device_ID>
    sudo zpool set autoexpand=off <pool_name>

Replace the following variables:

`<LXD_lib_dir>`
: `/var/snap/lxd/common/lxd/` if you are using the snap, or `/var/lib/lxd/` otherwise.

`<pool_name>`
: The name of your storage pool (for example, `my-pool`).

`<device_ID>`
: The ID of the ZFS device.
  Enter `sudo zpool status -vg <pool_name>` to find the ID.
```

````

## Resize a storage volume

To resize a storage volume, set its size configuration:

    lxc storage volume set <pool_name> <volume_name> size <new_size>

```{important}
- Growing a storage volume usually works (if the storage pool has sufficient storage).
- Shrinking a storage volume is only possible for storage volumes with content type `filesystem`.
  It is not guaranteed to work though, because you cannot shrink storage below its current used size.
- Shrinking a storage volume with content type `block` is not possible.

```
