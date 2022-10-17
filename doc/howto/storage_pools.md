---
discourse: 1333
---

(howto-storage-pools)=
# How to manage storage pools

See the following sections for instructions on how to create, configure, view and resize {ref}`storage-pools`.

(storage-create-pool)=
## Create a storage pool

LXD creates a storage pool during initialization.
You can add more storage pools later, using the same driver or different drivers.

To create a storage pool, use the following command:

    lxc storage create <pool_name> <driver> [configuration_options...]

Unless specified otherwise, LXD sets up loop-based storage with a sensible default size (20% of the free disk space, but at least 5 GiB and at most 30 GiB).

See the {ref}`storage-drivers` documentation for a list of available configuration options for each driver.

### Examples

See the following examples for how to create a storage pool using different storage drivers.

`````{tabs}

````{group-tab} Directory

Create a directory pool named `pool1`:

    lxc storage create pool1 dir

Use the existing directory `/data/lxd` for `pool2`:

    lxc storage create pool2 dir source=/data/lxd
````
````{group-tab} Btrfs

Create a loop-backed pool named `pool1`:

    lxc storage create pool1 btrfs

Use the existing Btrfs file system at `/some/path` for `pool2`:

    lxc storage create pool2 btrfs source=/some/path

Create a pool named `pool3` on `/dev/sdX`:

    lxc storage create pool3 btrfs source=/dev/sdX
````
````{group-tab} LVM

Create a loop-backed pool named `pool1` (the LVM volume group will also be called `pool1`):

    lxc storage create pool1 lvm

Use the existing LVM volume group called `my-pool` for `pool2`:

    lxc storage create pool2 lvm source=my-pool

Use the existing LVM thin pool called `my-pool` in volume group `my-vg` for `pool3`:

    lxc storage create pool3 lvm source=my-vg lvm.thinpool_name=my-pool

Create a pool named `pool4` on `/dev/sdX` (the LVM volume group will also be called `pool4`):

    lxc storage create pool4 lvm source=/dev/sdX

Create a pool named `pool5` on `/dev/sdX` with the LVM volume group name `my-pool`:

    lxc storage create pool5 lvm source=/dev/sdX lvm.vg_name=my-pool
````
````{group-tab} ZFS

Create a loop-backed pool named `pool1` (the ZFS zpool will also be called `pool1`):

    lxc storage create pool1 zfs

Create a loop-backed pool named `pool2` with the ZFS zpool name `my-tank`:

    lxc storage create pool2 zfs zfs.pool_name=my-tank

Use the existing ZFS zpool `my-tank` for `pool3`:

    lxc storage create pool3 zfs source=my-tank

Use the existing ZFS dataset `my-tank/slice` for `pool4`:

    lxc storage create pool4 zfs source=my-tank/slice

Create a pool named `pool5` on `/dev/sdX` (the ZFS zpool will also be called `pool5`):

    lxc storage create pool5 zfs source=/dev/sdX

Create a pool named `pool6` on `/dev/sdX` with the ZFS zpool name `my-tank`:

    lxc storage create pool6 zfs source=/dev/sdX zfs.pool_name=my-tank
````
````{group-tab} Ceph RBD

Create an OSD storage pool named `pool1` in the default Ceph cluster (named `ceph`):

    lxc storage create pool1 ceph

Create an OSD storage pool named `pool2` in the Ceph cluster `my-cluster`:

    lxc storage create pool2 ceph ceph.cluster_name=my-cluster

Create an OSD storage pool named `pool3` with the on-disk name `my-osd` in the default Ceph cluster:

    lxc storage create pool3 ceph ceph.osd.pool_name=my-osd

Use the existing OSD storage pool `my-already-existing-osd` for `pool4`:

    lxc storage create pool4 ceph source=my-already-existing-osd

Use the existing OSD erasure-coded pool `ecpool` and the OSD replicated pool `rpl-pool` for `pool5`:

    lxc storage create pool5 ceph source=rpl-pool ceph.osd.data_pool_name=ecpool
````
````{group-tab} CephFS

```{note}
When using the CephFS driver, you must create a CephFS file system beforehand.
This file system consists of two OSD storage pools, one for the actual data and one for the file metadata.
```

Use the existing CephFS file system `my-filesystem` for `pool1`:

    lxc storage create pool1 cephfs source=my-filesystem

Use the sub-directory `my-directory` from the `my-filesystem` file system for `pool2`:

    lxc storage create pool2 cephfs source=my-filesystem/my-directory

````
````{group-tab} Ceph Object

```{note}
When using the Ceph Object driver, you must have a running Ceph Object Gateway [`radosgw`](https://docs.ceph.com/en/latest/radosgw/) URL available beforehand.
```

Use the existing Ceph Object Gateway `https://www.example.com/radosgw` to create `pool1`:

    lxc storage create pool1 cephobject cephobject.radosgw.endpoint=https://www.example.com/radosgw
````
`````

(storage-pools-cluster)=
### Create a storage pool in a cluster

If you are running a LXD cluster and want to add a storage pool, you must create the storage pool for each cluster member separately.
The reason for this is that the configuration, for example, the storage location or the size of the pool, might be different between cluster members.

Therefore, you must first create a pending storage pool on each member with the `--target=<cluster_member>` flag and the appropriate configuration for the member.
Make sure to use the same storage pool name for all members.
Then create the storage pool without specifying the `--target` flag to actually set it up.

For example, the following series of commands sets up a storage pool with the name `my-pool` at different locations and with different sizes on three cluster members:

    lxc storage create my-pool zfs source=/dev/sdX size=10GB --target=vm01
    lxc storage create my-pool zfs source=/dev/sdX size=15GB --target=vm02
    lxc storage create my-pool zfs source=/dev/sdY size=10GB --target=vm03
    lxc storage create my-pool zfs

```{note}
For most storage drivers, the storage pools exist locally on each cluster member.
That means that if you create a storage volume in a storage pool on one member, it will not be available on other cluster members.

This behavior is different for Ceph-based storage pools (`ceph`, `cephfs` and `cephobject`) where each storage pool exists in one central location and therefore, all cluster members access the same storage pool with the same storage volumes.
```

## Configure storage pool settings

See the {ref}`storage-drivers` documentation for the available configuration options for each storage driver.

General keys for a storage pool (like `source`) are top-level.
Driver-specific keys are namespaced by the driver name.

Use the following command to set configuration options for a storage pool:

    lxc storage set <pool_name> <key> <value>

For example, to turn off compression during storage pool migration for a `dir` storage pool, use the following command:

    lxc storage set my-dir-pool rsync.compression false

You can also edit the storage pool configuration by using the following command:

    lxc storage edit <pool_name>

## View storage pools

You can display a list of all available storage pools and check their configuration.

Use the following command to list all available storage pools:

    lxc storage list

The resulting table contains the storage pool that you created during initialization (usually called `default` or `local`) and any storage pools that you added.

To show detailed information about a specific pool, use the following command:

    lxc storage show <pool_name>

(storage-resize-pool)=
## Resize a storage pool

If you need more storage, you can increase the size of your storage pool.

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
