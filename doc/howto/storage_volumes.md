(howto-storage-volumes)=
# How to manage storage volumes

```{youtube} https://www.youtube.com/watch?v=dvQ111pbqtk
```

See the following sections for instructions on how to create, configure, view and resize {ref}`storage-volumes`.

## View storage volumes

You can display a list of all available storage volumes and check their configuration.

`````{tabs}
````{group-tab} CLI

To list all available storage volumes, use the following command:

    lxc storage volume list

To display the storage volumes for all projects (not only the default project), add the `--all-projects` flag.

You can also display the storage volumes in a specific storage pool:

    lxc storage volume list my-pool

The resulting table contains, among other information, the {ref}`storage volume type <storage-volume-types>` and the {ref}`content type <storage-content-types>` for each storage volume.

```{note}
Custom storage volumes can use the same name as instance volumes. For example, you might have a container named `c1` with a container storage volume named `c1` and a custom storage volume named `c1`.
Therefore, to distinguish between instance storage volumes and custom storage volumes, all instance storage volumes must be referred to as `<volume_type>/<volume_name>` (for example, `container/c1` or `virtual-machine/vm`) in commands.
```

To show detailed configuration information about a specific volume, use the following command:

    lxc storage volume show my-pool custom/my-volume

To show state information about a specific volume, use the following command:

    lxc storage volume info my-pool virtual-machine/my-vm

In both commands, the default {ref}`storage volume type <storage-volume-types>` is `custom`, so you can leave out the `custom/` when displaying information about a custom storage volume.

````
```` {group-tab} UI

From the main navigation, select {guilabel}`Storage` > {guilabel}`Volumes`.
The resulting page displays a table of available volumes. You can sort volumes by their pool by clicking the {guilabel}`Pool` column header of the table.

````
`````

## Create a custom storage volume

When you create an instance, LXD automatically creates a storage volume that is used as the root disk for the instance.

You can add custom storage volumes to your instances.
Such custom storage volumes are independent of the instance, which means that they can be backed up separately and are retained until you delete them.
Custom storage volumes with content type `filesystem` can also be shared between different instances.

See {ref}`storage-volumes` for detailed information.

### Create the volume

`````{tabs}
````{group-tab} CLI

Use the following command to create a custom storage volume `vol1` of type `filesystem` in storage pool `my-pool`:

    lxc storage volume create my-pool vol1

By default, custom storage volumes use the `filesystem` {ref}`content type <storage-content-types>`.
To create a custom volume with content type `block`, add the `--type` flag:

    lxc storage volume create my-pool vol2 --type=block
````
```` {group-tab} UI


From the main navigation, select {guilabel}`Storage` > {guilabel}`Volumes`.

On the resulting page, click {guilabel}`Create volume` in the upper-right corner.

You can then configure the name and size of your storage volume.

You can select a content type from the {guilabel}`Content type` dropdown. Additional settings might appear, depending on the content type selected.

Click {guilabel}`Create` to create the storage pool.

```{figure} /images/storage/storage_volumes_create.png
:width: 80%
:alt: Create a storage volume in LXD
```

````
`````

(storage-attach-volume)=
### Attach the volume to an instance

After creating a custom storage volume, you can add it to one or more instances as a {ref}`disk device <devices-disk>`.

The following restrictions apply:

- Storage volumes of {ref}`content type <storage-content-types>` `block` or `iso` cannot be attached to containers, only to virtual machines.
- Storage volumes of {ref}`content type <storage-content-types>` `block` that don't have `security.shared` enabled cannot be attached to more than one instance at the same time.
  Attaching a `block` volume to more than one instance at a time risks data corruption.
- Storage volumes of {ref}`content type <storage-content-types>` `iso` are always read-only, and can therefore be attached to more than one virtual machine at a time without corrupting data.
- Storage volumes of {ref}`content type <storage-content-types>` `filesystem` can't be attached to virtual machines while they're running.
- You cannot attach a storage volume from a local storage pool (a pool that uses the {ref}`Directory <storage-dir>`, {ref}`Btrfs <storage-btrfs>`, {ref}`ZFS <storage-zfs>`, or {ref}`LVM <storage-lvm>` driver) to an instance that has {config:option}`instance-migration:migration.stateful` set to `true`. You must set {config:option}`instance-migration:migration.stateful` to `false` on the instance. Note that doing so makes the instance ineligible for {ref}`live migration <live-migration-vms>`.

`````{tabs}
````{group-tab} CLI

Use the following command to attach a custom storage volume `fs-vol` with content type `filesystem` to instance `c1`.
`/data` is the mount point for the storage volume inside the instance:

    lxc storage volume attach my-pool fs-vol c1 /data

Custom storage volumes with the content type `block` do not take a mount point:

    lxc storage volume attach my-pool bl-vol vm1

By default, custom storage volumes are added to the instance with the volume name as the {ref}`device <devices>` name.
If you want to use a different device name, you can add it to the command:

    lxc storage volume attach my-pool fs-vol c1 filesystem-volume /data
    lxc storage volume attach my-pool bl-vol vm1 block-volume

#### Attach the volume as a device

The [`lxc storage volume attach`](lxc_storage_volume_attach.md) command is a shortcut for adding a disk device to an instance.
The following commands have the same effect as the corresponding commands above:

    lxc config device add c1 filesystem-volume disk pool=my-pool source=fs-vol path=/data
    lxc config device add vm1 block-volume disk pool=my-pool source=bl-vol

This allows adding further configuration for the device.
See {ref}`disk device <devices-disk>` for all available device options.

(storage-configure-IO)=
#### Configure I/O options

When you attach a storage volume to an instance as a {ref}`disk device <devices-disk>`, you can configure I/O limits for it.
To do so, set the {config:option}`device-disk-device-conf:limits.read`, {config:option}`device-disk-device-conf:limits.write` or {config:option}`device-disk-device-conf:limits.max` options to the corresponding limits.
See the {ref}`devices-disk` reference for more information.

The limits are applied through the Linux `blkio` cgroup controller, which makes it possible to restrict I/O at the disk level (but nothing finer grained than that).

```{note}
Because the limits apply to a whole physical disk rather than a partition or path, the following restrictions apply:

- Limits will not apply to file systems that are backed by virtual devices (for example, device mapper).
- If a file system is backed by multiple block devices, each device will get the same limit.
- If two disk devices that are backed by the same disk are attached to the same instance, the limits of the two devices will be averaged.
```

All I/O limits only apply to actual block device access.
Therefore, consider the file system's own overhead when setting limits.
Access to cached data is not affected by the limit.

For VMs the way the disk is exposed to the guest and its behavior can be configured.
To do so, set the {config:option}`device-disk-device-conf:io.bus`, {config:option}`device-disk-device-conf:io.cache` or {config:option}`device-disk-device-conf:io.threads` options.
See the {ref}`devices-disk` reference for more information.

````

```` {group-tab} UI
You can attach a storage volume to an existing instance, or when creating a new instance:

- For an existing instance, select {guilabel}`Instances` from the main navigation, then select the target instance to view its details page. Open its {guilabel}`Configuration` tab.
- For a new instance, you must first select a base image during the instance creation process.

In either scenario, then select {guilabel}`Disk` from the {guilabel}`Devices` section of the secondary menu.

Click {guilabel}`Attach disk device`.

```{figure} /images/storage/storage_volumes_attach_to_instance_1.png
:width: 80%
:alt: Attach a storage volume to an instance - Disk configuration page
```

The resulting modal allows you to choose your disk type. Select {guilabel}`Attach custom volume`:

```{figure} /images/storage/storage_volumes_attach_to_instance_2.png
:width: 80%
:alt: Attach a storage volume to an instance - Attach disk device modal
```

Next, you can either select a pre-existing volume to attach to the instance by clicking its corresponding {guilabel}`Select` button, or create a new custom volume by clicking {guilabel}`Create volume`:

```{figure} /images/storage/storage_volumes_attach_to_instance_3.png
:width: 80%
:alt: Attach a storage volume to an instance - Attach custom volume modal
```

Once the modal closes, you might be required to add a mount point file path in the {guilabel}`Mount point` field.
Finally, you can save your instance changes. If you are in the instance creation process, create your instance by clicking {guilabel}`Create`.


````
`````

(storage-volume-special)=
### Use the volume for backups or images

Instead of attaching a custom volume to an instance as a disk device, you can also use it as a special kind of volume to store {ref}`backups <backups>` or {ref}`images <about-images>`.

`````{tabs}
````{group-tab} CLI

To do so, you must set the corresponding {ref}`server configuration <server-options-misc>`:

- To use a custom volume `my-backups-volume` to store the backup tarballs:

      lxc config set storage.backups_volume=my-pool/my-backups-volume

- To use a custom volume `my-images-volume` to store the image tarballs:

      lxc config set storage.images_volume=my-pool/my-images-volume

````
````{group-tab} UI

To use a volume to store backups or images, select {guilabel}`Settings` from the main navigation. From this page, set the value of the {guilabel}`storage.backups_volume` key or the {guilabel}`storage.images_volume` key to the name of the target storage volume, then select {guilabel}`Save`.


````
`````

(storage-configure-volume)=
## Configure storage volume settings

See the {ref}`storage-drivers` documentation for a list of available storage volume configuration options for each driver.

`````{tabs}
````{group-tab} CLI

To set the maximum size of custom storage volume `my-volume` to 1 GiB, use the following command:

    lxc storage volume set my-pool my-volume size=1GiB

The default {ref}`storage volume type <storage-volume-types>` is `custom`, but other volume types can be configured by using the `<volume_type>/<volume_name>` syntax.

To set the snapshot expiry time for virtual machine `my-vm` to one month, use the following command:

    lxc storage volume set my-pool virtual-machine/my-vm snapshots.expiry=1M

You can also edit the storage volume configuration as YAML in a text editor:

    lxc storage volume edit my-pool virtual-machine/my-vm

(storage-configure-vol-default)=
### Configure default values for storage volumes

You can define default volume configurations for a storage pool.
To do so, set a storage pool configuration with a `volume` prefix: `volume.<KEY>=<VALUE>`.

This value is used for all new storage volumes in the pool, unless it is explicitly overridden.
In general, the defaults set at the storage pool level can be overridden through a volume's configuration.
For storage volumes of {ref}`type <storage-volume-types>` `container` or `virtual-machine`, the pool's volume configuration can be overridden via the instance configuration.

For example, to set the default volume size for `my-pool`, use the following command:

    lxc storage set my-pool volume.size=15GiB

## Attach instance root volumes to other instances
Virtual-machine root volumes can be attached as disk devices to other virtual machines.
In order to prevent concurrent access, `security.protection.start` must be set on
an instance before its root volume can be attached to another virtual-machine.

```{caution}
Because instances created from the same image share the same partition and file system
UUIDs and labels, booting an instance with two root file systems mounted may result
in the wrong root file system being used. This may result in unexpected behavior
or data loss. **It is strongly recommended to only attach virtual-machine root
volumes to other virtual machines when the target virtual-machine is running.**
```

Assuming `vm1` is stopped and `vm2` is running, attach the `virtual-machine/vm1` storage
volume to `vm2`:

    lxc config set vm1 security.protection.start=true
    lxc storage volume attach my-pool virtual-machine/vm1 vm2

`virtual-machine/vm1` must be detached from `vm2` before `security.protection.start`
can be unset from `vm1`:

    lxc storage volume detach my-pool virtual-machine/vm1 vm2
    lxc config unset vm1 security.protection.start

`security.shared` can also be used on `virtual-machine` volumes to enable concurrent
access. Note that concurrent access to block volumes may result in data loss.

### Attaching virtual machine snapshots to other instances
Virtual-machine snapshots can also be attached to instances with the
{config:option}`device-disk-device-conf:source.snapshot` disk device
configuration key.

    lxc config device add v1 v2-root-snap0 disk pool=my-pool source=vm2 source.type=virtual-machine source.snapshot=snap0

## Resize a storage volume

If you need more storage in a volume, you can increase the size of your storage volume.
In some cases, it is also possible to reduce the size of a storage volume.

To adjust a storage volume's quota, set its `size` configuration.
For example, to resize `my-volume` in storage pool `my-pool` to `15GiB`, use the following command:

    lxc storage volume set my-pool my-volume size=15GiB

```{important}
- Growing a volume is possible if the storage pool has sufficient storage.
- Shrinking a storage volume is only possible for storage volumes with content type `filesystem`.
  It is not guaranteed to work though, because you cannot shrink storage below its current used size.
- Shrinking a storage volume with content type `block` is not possible.

```

````
```` {group-tab} UI

To configure a custom storage volume, select {guilabel}`Storage` > {guilabel}`Volumes` from the main navigation. Next, click the name of your target storage volume to view its details page.

```{note}
Volume details pages are only available for volumes of type Custom. Volumes of other types—such as Instance root disks—can also be accessed from the Volumes page and redirect to their respective entity overview or list page.

To sort the Volumes table by type, you can click the {guilabel}`Content type` column header.
```

On the volume's overview page, go to the {guilabel}`Configuration` tab. Here, you can configure settings such as the storage volume size. Further configuration options can be found in the secondary menu.
After making changes, click the {guilabel}`Save changes` button. This button also displays the number of changes you have made.

````
`````

## Create a storage volume in a cluster

For most storage drivers, custom storage volumes are not replicated across the cluster and exist only on the member for which they were created.
This behavior differs for remote storage pools (`ceph`, `cephfs` and `powerflex`), where volumes are available from any cluster member.

`````{tabs}
````{group-tab} CLI

To add a custom storage volume on a cluster member, add the `--target` flag:

```bash
lxc storage volume create <pool-name> <volume-name> --target=<member-name>
```

Example:
```bash
lxc storage volume create my-pool my-volume --target=my-member
```

To create a custom storage volume of type `iso`, use `import` instead of `create`:

```bash
lxc storage volume import <pool-name> <path-to-iso> <volume-name> --type=iso
```


````
```` {group-tab} UI

To create a storage volume in a clustered environment, select {guilabel}`Storage` > {guilabel}`Volumes` from the main navigation. On the Volumes page, click {guilabel}`Create volume` in the upper-right corner.

On the volume creation page, select the cluster member on which to base the storage volume from the {guilabel}`Cluster member` dropdown. This dropdown is only available if the storage pool selected for this volume is cluster-member specific, rather than shared across the cluster.

```{figure} /images/storage/storage_volumes_create_clustered.png
:width: 80%
:alt: Create a custom storage volume in a clustered environment
```

````
`````

To find out more about clusters in LXD, see:

- {ref}`Clustering how-to guides <clustering>`
- {ref}`An explanation about clusters <exp-clusters>`
