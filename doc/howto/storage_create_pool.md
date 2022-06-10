(storage_create_pool)=
# How to create a storage pool

LXD creates a storage pool during initialization.
You can add more storage pools later, using the same driver or different drivers.

To create a storage pool, use the following command:

    lxc storage create <pool_name> <driver> [configuration_options...]

See the {ref}`storage-drivers` documentation for a list of available configuration options for each driver.

## Examples

See the following examples for how to create a storage pool using different storage drivers.

````{tabs}

```{group-tab} Directory

Create a directory pool named `pool1`:

    lxc storage create pool1 dir

Use the existing directory `/data/lxd` for `pool2`:

    lxc storage create pool2 dir source=/data/lxd
```
```{group-tab} Btrfs

Create a loop-backed pool named `pool1`:

    lxc storage create pool1 btrfs

Use the existing Btrfs file system at `/some/path` for `pool2`:

    lxc storage create pool2 btrfs source=/some/path

Create a pool named `pool3` on `/dev/sdX`:

    lxc storage create pool3 btrfs source=/dev/sdX
```
```{group-tab} LVM

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
```
```{group-tab} ZFS

Create a loop-backed pool named `pool1` (the ZFS zpool will also be called `pool1`):

    lxc storage create pool1 zfs

Create a loop-backed pool named `pool2` with the ZFS zpool name `my-tank`:

    lxc storage create pool2 zfs zfs.pool_name=my-tank

Use the existing ZFS zpool `my-tank` for `pool3`:

    lxc storage create pool3 zfs source=my-tank

Use the existing ZFS data set `my-tank/slice` for `pool4`:

    lxc storage create pool4 zfs source=my-tank/slice

Create a pool named `pool5` on `/dev/sdX` (the ZFS zpool will also be called `pool5`):

    lxc storage create pool1 zfs source=/dev/sdX

Create a pool named `pool6` on `/dev/sdX` with the ZFS zpool name `my-tank`:

    lxc storage create pool6 zfs source=/dev/sdX zfs.pool_name=my-tank
```
```{group-tab} Ceph

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
```
```{group-tab} CephFS

Create a storage pool named `pool1` in the default Ceph cluster (named `ceph`):

    lxc storage create pool1 cephfs

Create a storage pool named `pool2` in the Ceph cluster `my-cluster`:

    lxc storage create pool2 cephfs cephfs.cluster_name=my-cluster

Use the existing storage pool `my-filesystem` for `pool3`:

    lxc storage create pool3 cephfs source=my-filesystem

Use the sub-directory `my-directory` from the `my-filesystem` pool for `pool4`:

    lxc storage create pool4 cephfs source=my-filesystem/my-directory

```
````

## Create a storage pool in a cluster

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

This behavior is different for Ceph-based storage pools (`ceph` and `cephfs`) where each storage pool exists in one central location and therefore, all cluster members access the same storage pool with the same storage volumes.
```
