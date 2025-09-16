---
discourse: lxc:[How&#32;to&#32;resize&#32;ZFS&#32;used&#32;in&#32;LXD](1333)
---

(howto-storage-pools)=
# How to manage storage pools

See the following sections for instructions on how to create, configure, view and resize {ref}`storage-pools`.

(storage-create-pool)=
## Create a storage pool

LXD creates a storage pool during initialization. You can add more storage pools later, using the same driver or different drivers.

For Ceph-based storage pools, first see the {ref}`howto-storage-pools-ceph-requirements` section.

`````{tabs}
````{group-tab} CLI
To create a storage pool, use the following command:

    lxc storage create <pool_name> <driver> [configuration_options...]
See the {ref}`storage-drivers` documentation for a list of available configuration options for each driver.

````

````{group-tab} UI
To create a storage pool, select {guilabel}`Pools` from the {guilabel}`Storage` section of the main navigation.

On the resulting screen, click {guilabel}`Create pool` in the upper right corner.

From this screen, you can configure the name and description of your storage pool.
You can select a storage driver from the {guilabel}`Driver` dropdown. Additional settings might appear, depending on the storage driver selected.

Click {guilabel}`Create` to create the storage pool.


```{figure} /images/storage/storage_pools_create.png
:width: 80%
:alt: Create a storage pool in LXD
```
````
`````

By default, LXD sets up loop-based storage with a sensible default size/quota: 20% of the free disk space, with a minimum of 5 GiB and a maximum of 30 GiB.

### Examples

`````{tabs}
````{group-tab} CLI

The following examples demonstrate how to create a storage pool using different types of storage drivers.

#### Create a directory pool

Create a directory pool named `pool1`:

    lxc storage create pool1 dir

Use the existing directory `/data/lxd` for `pool2`:

    lxc storage create pool2 dir source=/data/lxd

#### Create a Btrfs pool

Create a loop-backed pool named `pool1`:

    lxc storage create pool1 btrfs

Use the existing Btrfs file system at `/some/path` for `pool2`:

    lxc storage create pool2 btrfs source=/some/path

Create a pool named `pool3` on `/dev/sdX`:

    lxc storage create pool3 btrfs source=/dev/sdX

#### Create an LVM pool

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

#### Create a ZFS pool

Create a loop-backed pool named `pool1` (the ZFS zpool will also be called `pool1`):

    lxc storage create pool1 zfs

Create a loop-backed pool named `pool2` with the ZFS zpool name `my-tank`:

    lxc storage create pool2 zfs zfs.pool_name=my-tank

Use the existing ZFS zpool `my-tank` for `pool3`:

    lxc storage create pool3 zfs source=my-tank

Use the existing ZFS dataset `my-tank/slice` for `pool4`:

    lxc storage create pool4 zfs source=my-tank/slice

Use the existing ZFS dataset `my-tank/zvol` for `pool5` and configure it to use ZFS block mode:

    lxc storage create pool5 zfs source=my-tank/zvol volume.zfs.block_mode=yes

Create a pool named `pool6` on `/dev/sdX` (the ZFS zpool will also be called `pool6`):

    lxc storage create pool6 zfs source=/dev/sdX

Create a pool named `pool7` on `/dev/sdX` with the ZFS zpool name `my-tank`:

    lxc storage create pool7 zfs source=/dev/sdX zfs.pool_name=my-tank

#### Ceph-based storage pools

For Ceph-based storage pools, first see the {ref}`howto-storage-pools-ceph-requirements`.

##### Create a Ceph RBD pool

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

##### Create a CephFS pool

```{note}
Each CephFS file system consists of two OSD storage pools, one for the actual data and one for the file metadata.
```

Use the existing CephFS file system `my-filesystem` for `pool1`:

    lxc storage create pool1 cephfs source=my-filesystem

Use the sub-directory `my-directory` from the `my-filesystem` file system for `pool2`:

    lxc storage create pool2 cephfs source=my-filesystem/my-directory

Create a CephFS file system `my-filesystem` with a data pool called `my-data` and a metadata pool called `my-metadata` for `pool3`:

    lxc storage create pool3 cephfs source=my-filesystem cephfs.create_missing=true cephfs.data_pool=my-data cephfs.meta_pool=my-metadata

##### Create a Ceph Object pool

A RADOS Gateway endpoint is required for a {ref}`Ceph Object <storage-cephobject>` storage pool. See: {ref}`howto-storage-pools-ceph-requirements-radosgw`.

For a non-clustered LXD server, create `pool1` by passing in a Ceph Object Gateway endpoint (the endpoint shown below is only an example; you must use your own):

```bash
lxc storage create pool1 cephobject cephobject.radosgw.endpoint=http://192.0.2.10:8080
```

If your LXD server is clustered, such as in a [MicroCloud](https://canonical.com/microcloud) deployment, see: {ref}`storage-pools-cluster`.

#### Create a Dell PowerFlex pool

Create a storage pool named `pool1` using the PowerFlex pool `sp1` in the protection domain `pd1`:

    lxc storage create pool1 powerflex powerflex.pool=sp1 powerflex.domain=pd1 powerflex.gateway=https://powerflex powerflex.user.name=lxd powerflex.user.password=foo

Create a storage pool named `pool2` using the ID of PowerFlex pool `sp1`:

    lxc storage create pool2 powerflex powerflex.pool=<ID of sp1> powerflex.gateway=https://powerflex powerflex.user.name=lxd powerflex.user.password=foo

Create a storage pool named `pool3` that uses PowerFlex volume snapshots (see {ref}`storage-powerflex-limitations`) when creating volume copies:

    lxc storage create pool3 powerflex powerflex.snapshot_copy=true powerflex.pool=<id of sp1> powerflex.gateway=https://powerflex powerflex.user.name=lxd powerflex.user.password=foo

Create a storage pool named `pool4` that uses a PowerFlex gateway with a certificate that is not trusted:

    lxc storage create pool4 powerflex powerflex.gateway.verify=false powerflex.pool=<id of sp1> powerflex.gateway=https://powerflex powerflex.user.name=lxd powerflex.user.password=foo

Create a storage pool named `pool5` that explicitly uses the PowerFlex SDC:

    lxc storage create pool5 powerflex powerflex.mode=sdc powerflex.pool=<id of sp1> powerflex.gateway=https://powerflex powerflex.user.name=lxd powerflex.user.password=foo

#### Create a Pure Storage pool

Create a storage pool named `pool1` that uses NVMe/TCP by default:

    lxc storage create pool1 pure pure.gateway=https://<pure-storage-address> pure.api.token=<pure-storage-api-token>

Create a storage pool named `pool2` that uses a Pure Storage gateway with a certificate that is not trusted:

    lxc storage create pool2 pure pure.gateway=https://<pure-storage-address> pure.gateway.verify=false pure.api.token=<pure-storage-api-token>

Create a storage pool named `pool3` that uses iSCSI to connect to Pure Storage array:

    lxc storage create pool3 pure pure.gateway=https://<pure-storage-address> pure.api.token=<pure-storage-api-token> pure.mode=iscsi

Create a storage pool named `pool4` that uses NVMe/TCP to connect to Pure Storage array via specific target addresses:

    lxc storage create pool4 pure pure.gateway=https://<pure-storage-address> pure.api.token=<pure-storage-api-token> pure.mode=nvme pure.target=<target_address_1>,<target_address_2>

#### Create a HPE Alletra pool

Create a storage pool named `pool1` that uses NVMe/TCP by default:

    lxc storage create pool1 alletra alletra.wsapi=https://<alletra-storage-address> alletra.user.name=<alletra-storage-username> alletra.user.password=<alletra-storage-password>

Create a storage pool named `pool2` that uses a HPE Alletra gateway with a certificate that is not trusted:

    lxc storage create pool2 alletra alletra.wsapi=https://<alletra-storage-address> alletra.wsapi.verify=false alletra.user.name=<alletra-storage-username> alletra.user.password=<alletra-storage-password>

Create a storage pool named `pool3` that uses NVMe/TCP to connect to Pure Storage array via specific target addresses:

    lxc storage create pool3 alletra alletra.wsapi=https://<alletra-storage-address> alletra.user.name=<alletra-storage-username> alletra.user.password=<alletra-storage-password> alletra.mode=nvme alletra.target=<target_address_1>,<target_address_2>

````

````{group-tab} UI

You can select a storage driver from the {guilabel}`Driver` dropdown.

Some storage drivers offer additional settings. Click the driver name in the secondary menu to further configure the storage pool.

```{figure} /images/storage/storage_pools_create_ZFS_driver.png
:width: 80%
:alt: Storage pool options for driver ZFS in LXD-UI
```

See the {ref}`storage-drivers` documentation for a list of available configuration options for each driver.

````
`````

(storage-pools-cluster)=
## Create a storage pool in a cluster

If you are running a LXD cluster and want to add a storage pool, you must create the storage pool for each cluster member separately.
The reason for this is that the configuration, for example, the storage location or the size of the pool, might be different between cluster members.

`````{tabs}
````{group-tab} CLI

To create a storage pool via the CLI, start by creating a pending storage pool on each member with the `--target=<cluster_member>` flag and the appropriate configuration for the member.

Make sure to use the same storage pool name for all members.
Then create the storage pool without specifying the `--target` flag to actually set it up.

Also see {ref}`cluster-config-storage`.

```{note}
For most storage drivers, the storage pools exist locally on each cluster member.
That means that if you create a storage volume in a storage pool on one member, it will not be available on other cluster members.

This behavior is different for Ceph-based storage pools (`ceph`, `cephfs` and `cephobject`) where each storage pool exists in one central location and therefore, all cluster members access the same storage pool with the same storage volumes.
```

### Examples

See the following examples for different storage drivers for instructions on how to create local or remote storage pools in a cluster.

#### Create a local storage pool

Create a storage pool named `my-pool` using the ZFS driver at different locations and with different sizes on three cluster members:

```{terminal}
:input: lxc storage create my-pool zfs source=/dev/sdX size=10GiB --target=vm01
Storage pool my-pool pending on member vm01
:input: lxc storage create my-pool zfs source=/dev/sdX size=15GiB --target=vm02
Storage pool my-pool pending on member vm02
:input: lxc storage create my-pool zfs source=/dev/sdY size=10GiB --target=vm03
Storage pool my-pool pending on member vm03
:input: lxc storage create my-pool zfs
Storage pool my-pool created
```

#### Create a remote or distributed storage pool

Create a storage pool named `my-ceph-pool` using the {ref}`Ceph RBD driver <storage-ceph>` and the on-disk name `my-osd` on three cluster members.
Because the {config:option}`storage-ceph-pool-conf:ceph.osd.pool_name` configuration setting isn't member-specific, it must be set when creating the actual storage pool:

```{terminal}
:input: lxc storage create my-ceph-pool ceph --target=vm01
Storage pool my-ceph-pool pending on member vm01
:input: lxc storage create my-ceph-pool ceph --target=vm02
Storage pool my-ceph-pool pending on member vm02
:input: lxc storage create my-ceph-pool ceph --target=vm03
Storage pool my-ceph-pool pending on member vm03
:input: lxc storage create my-ceph-pool ceph ceph.osd.pool_name=my-osd
Storage pool my-ceph-pool created
```

Create a storage pool named `my-cephobject-pool` using the {ref}`Ceph Object driver <storage-cephobject>` and a preconfigured {ref}`RADOS Gateway endpoint <howto-storage-pools-ceph-requirements-radosgw>` (the endpoint shown below is only an example):
```{terminal}
:input: lxc storage create my-cephobject-pool cephobject --target=vm01
Storage pool my-cephobject-pool pending on member vm01
:input: lxc storage create my-cephobject-pool cephobject --target=vm02
Storage pool my-cephobject-pool pending on member vm02
:input: lxc storage create my-cephobject-pool cephobject --target=vm03
Storage pool my-cephobject-pool pending on member vm03
:input: lxc storage create my-cephobject-pool cephobject cephobject.radosgw.endpoint=http://192.0.2.10:8080
Storage pool my-cephobject-pool created
```

Create a storage pool named `my-powerflex-pool` using the {ref}`Dell PowerFlex driver <storage-powerflex>` in SDC mode and the pool `sp1` in protection domain `pd1`:

```{terminal}
:input: lxc storage create my-powerflex-pool powerflex --target=vm01
Storage pool my-powerflex-pool pending on member vm01
:input: lxc storage create my-powerflex-pool powerflex --target=vm02
Storage pool my-powerflex-pool pending on member vm02
:input: lxc storage create my-powerflex-pool powerflex --target=vm03
Storage pool my-powerflex-pool pending on member vm03
:input: lxc storage create my-powerflex-pool powerflex powerflex.mode=sdc powerflex.pool=sp1 powerflex.domain=pd1 powerflex.gateway=https://powerflex powerflex.user.name=lxd powerflex.user.password=foo
Storage pool my-powerflex-pool created
```

Create a storage pool named `my-purestorage-pool` using the {ref}`Pure Storage driver <storage-pure>`:

```{terminal}
:input: lxc storage create my-purestorage-pool pure --target=vm01
Storage pool my-purestorage-pool pending on member vm01
:input: lxc storage create my-purestorage-pool pure --target=vm02
Storage pool my-purestorage-pool pending on member vm02
:input: lxc storage create purestorage-pool pure --target=vm03
Storage pool purestorage-pool pending on member vm03
:input: lxc storage purestorage-pool pure pure.gateway=https://<pure-storage-address> pure.api.token=<pure-storage-api-token>
Storage pool purestorage-pool created
```

Create a storage pool named `my-alletrastorage-pool` using the {ref}`HPE Alletra driver <storage-alletra>`:

```{terminal}
:input: lxc storage create my-alletrastorage-pool alletra --target=vm01
Storage pool my-alletrastorage-pool pending on member vm01
:input: lxc storage create my-alletrastorage-pool alletra --target=vm02
Storage pool my-alletrastorage-pool pending on member vm02
:input: lxc storage create my-alletrastorage-pool alletra --target=vm03
Storage pool my-alletrastorage-pool pending on member vm03
:input: lxc storage my-alletrastorage-pool alletra alletra.wsapi=https://<alletra-storage-address> alletra.user.name=<alletra-storage-username> alletra.user.password=<alletra-storage-password>
Storage pool my-alletrastorage-pool created
```

````
````{group-tab} UI

To create a storage pool in a cluster, select {guilabel}`Pools` from the {guilabel}`Storage` section of the main navigation, then click {guilabel}`Create pool` in the upper right corner.

On the resulting page, configure the storage pool's name and description. Depending on the selected driver, some settings can be configured per cluster member or applied globally to the cluster.

Finally, click {guilabel}`Create` to create the storage pool.

```{figure} /images/storage/storage_pools_create_clustered_pool.png
:width: 80%
:alt: Create a storage pool in a clustered LXD environment
```


````
`````

## Configure storage pool settings

See the {ref}`storage-drivers` documentation for the available configuration options for each storage driver.

General keys for a storage pool (like `source`) are top-level.
Driver-specific keys are namespaced by the driver name.

`````{tabs}
````{group-tab} CLI

Use the following command to set configuration options for a storage pool:

    lxc storage set <pool_name> <key> <value>

For example, to turn off compression during storage pool migration for a `dir` storage pool, use the following command:

    lxc storage set my-dir-pool rsync.compression false

You can also edit the storage pool configuration by using the following command:

    lxc storage edit <pool_name>

````
```` {group-tab} UI

To configure a storage pool, select {guilabel}`Pools` from the {guilabel}`Storage` section of the Main navigation.

The resulting screen shows a list of existing storage pools. Click a pool's name to access its details.

Go to the {guilabel}`Configuration` tab. Here, you can configure settings such as the storage pool description.

After making changes, click the {guilabel}`Save changes` button. This button also displays the number of changes you have made.
`````

## View storage pools

You can display a list of all available storage pools and check their configuration.

`````{tabs}
````{group-tab} CLI

Use the following command to list all available storage pools:

    lxc storage list

The resulting table contains the storage pool that you created during initialization (usually called `default` or `local`) and any storage pools that you added.

To show detailed information about a specific pool, use the following command:

    lxc storage show <pool_name>

To see usage information for a specific pool, run the following command:

    lxc storage info <pool_name>

````
```` {group-tab} UI

To view available storage pools in the UI, select {guilabel}`Pools` from the {guilabel}`Storage` section of the main navigation.

````
`````

(storage-resize-pool)=
## Resize a storage pool

If you need more storage, you can increase the size (quota) of your storage pool. You can only grow the pool (increase its size), not shrink it.

`````{tabs}
````{group-tab} CLI

In the CLI, resize a storage pool by changing the `size` configuration key:

    lxc storage set <pool_name> size=<new_size>

This will only work for loop-backed storage pools that are managed by LXD.

````
```` {group-tab} UI

To resize a storage pool in the UI, select {guilabel}`Pools` from the {guilabel}`Storage ` section of the main navigation.

Click the name of a storage pool to open its details page, then go to its {guilabel}`Configuration` tab. Edit the {guilabel}`Size` field.

After making changes, click the {guilabel}`Save changes` button. This button also displays the number of changes you have made.

In clustered environments, the {guilabel}`Size` field appears as a per-member selector, allowing you to configure the size for each cluster member.

```{figure} /images/storage/storage_pools_create_clustered_pool_size_config.png
:width: 80%
:alt: Configuring storage pools sizes within a clustered environment.
```

````
`````

(howto-storage-pools-ceph-requirements)=
## Requirements for Ceph-based storage pools

For Ceph-based storage pools, the requirements below must be met before you can {ref}`storage-create-pool` or {ref}`storage-pools-cluster`.

(howto-storage-pools-ceph-requirements-cluster)=
### Ceph cluster

Before you can create a storage pool that uses the {ref}`Ceph RBD <storage-ceph>`, {ref}`CephFS <storage-cephfs>`, or {ref}`Ceph Object <storage-cephobject>` driver, you must have access to a [Ceph](https://ceph.io) cluster.

To deploy a Ceph cluster, we recommend using [MicroCloud](https://snapcraft.io/microcloud). If you have completed the default MicroCloud setup, you already have a Ceph cluster deployed through MicroCeph, so this requirement is met. MicroCeph is a lightweight way of deploying and managing a Ceph cluster.

If you do not use MicroCloud, set up a standalone deployment of [MicroCeph](https://snapcraft.io/microceph) before you continue.

(howto-storage-pools-ceph-requirements-radosgw)=
### Ceph Object and `radosgw`

Storage pools that use the {ref}`Ceph Object driver <storage-cephobject>` require a Ceph cluster with the RADOS Gateway (also known as RGW or `radosgw`) enabled.

(howto-storage-pools-ceph-requirements-radosgw-check)=
#### Check if `radosgw` is already enabled

To check if the RADOS Gateway is already enabled in MicroCeph, run this command from one of its cluster members:

```bash
microceph status
```

In the output, look for a cluster member with `rgw` in its `Services` list.

Example:

```{terminal}
:input: microceph status
:user: root
:host: micro1

MicroCeph deployment summary:
- micro1 (192.0.2.10)
  Services: mds, mgr, mon, rgw, osd
  Disks: 1
- micro2 (192.0.2.20)
  Services: mds, mgr, mon, osd
  Disks: 1
```

In the output above, notice `rgw` in the list of `Services` for `micro1`. This means that this cluster member is running the RADOS Gateway.

Look for `rgw` in your output. If you do not see it, you must {ref}`howto-storage-pools-ceph-requirements-radosgw-enable`.

If you do see it, you'll need the corresponding port number. On the cluster member with the `rgw` service, run:

```bash
sudo ss -ltnp | grep radosgw
```

Example:

```{terminal}
:input: sudo ss -ltnp | grep radosgw
:user: root
:host: micro1

LISTEN 0      4096         0.0.0.0:8080      0.0.0.0:*    users:(("radosgw",pid=11345,fd=60))
LISTEN 0      4096            [::]:8080         [::]:*    users:(("radosgw",pid=11345,fd=61))
```

The output above shows that the `radosgw` port number is `8080`.

(howto-storage-pools-ceph-requirements-radosgw-enable)=
#### Enable `radosgw`

If you did not find `rgw` in the `Services` list for any of your cluster members in the output from `microceph status`, then you must enable the RADOS Gateway. On one of the Ceph cluster members, run:

```bash
sudo microceph enable rgw --port 8080
```

We include the `--port 8080` flag because if unspecified, the default port is `80`. This default is a commonly used port number that can often cause conflicts with other services. You are not required to use `8080` — if needed, use a different port number.

(howto-storage-pools-ceph-requirements-radosgw-endpoint)=
#### The RADOS Gateway endpoint

The full RADOS Gateway endpoint includes the HTTP protocol, the IP address of the Ceph cluster member where the `rgw` service is enabled, and the port number specified. Example: `http://192.0.2.10:8080`.
