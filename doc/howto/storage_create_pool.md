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

 - Create a new directory pool called "pool1".

```bash
lxc storage create pool1 dir
```

 - Use an existing directory for "pool2".

```bash
lxc storage create pool2 dir source=/data/lxd
```
```
```{group-tab} Btrfs

 - Create loop-backed pool named "pool1".

```bash
lxc storage create pool1 btrfs
```

 - Create a new pool called "pool1" using an existing Btrfs filesystem at `/some/path`.

```bash
lxc storage create pool1 btrfs source=/some/path
```

 - Create a new pool called "pool1" on `/dev/sdX`.

```bash
lxc storage create pool1 btrfs source=/dev/sdX
```
```
```{group-tab} LVM

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
```
```{group-tab} ZFS

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
```
```{group-tab} Ceph

- Create a osd storage pool named "pool1" in the Ceph cluster "ceph".

```bash
lxc storage create pool1 ceph
```

- Create a osd storage pool named "pool1" in the Ceph cluster "my-cluster".

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

- Use the existing osd erasure coded pool "ecpool" and osd replicated pool "rpl-pool".

```bash
lxc storage create pool1 ceph source=rpl-pool ceph.osd.data_pool_name=ecpool
```
```
```{group-tab} CephFS

```
````
