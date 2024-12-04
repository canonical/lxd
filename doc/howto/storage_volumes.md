(howto-storage-volumes)=
# How to manage storage volumes

```{youtube} https://www.youtube.com/watch?v=dvQ111pbqtk
```

See the following sections for instructions on how to create, configure, view and resize {ref}`storage-volumes`.

## View storage volumes

You can display a list of all available storage volumes and check their configuration.

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

## Create a custom storage volume

When you create an instance, LXD automatically creates a storage volume that is used as the root disk for the instance.

You can add custom storage volumes to your instances.
Such custom storage volumes are independent of the instance, which means that they can be backed up separately and are retained until you delete them.
Custom storage volumes with content type `filesystem` can also be shared between different instances.

See {ref}`storage-volumes` for detailed information.

### Create the volume

Use the following command to create a custom storage volume `vol1` of type `filesystem` in storage pool `my-pool`:

    lxc storage volume create my-pool vol1

By default, custom storage volumes use the `filesystem` {ref}`content type <storage-content-types>`.
To create a custom volume with content type `block`, add the `--type` flag:

    lxc storage volume create my-pool vol2 --type=block

```{note}
For most storage drivers, custom storage volumes are not replicated across the cluster and exist only on the member for which they were created.
This behavior is different for remote storage pools (`ceph`, `cephfs` and `powerflex`), where volumes are available from any cluster member.
```

To add a custom storage volume on cluster member `member1`, add the `--target` flag:

    lxc storage volume create my-pool vol3 --target=member1

To create a custom storage volume of type `iso`, use `import` instead of `create`:

    lxc storage volume import my-pool <iso_path> vol4 --type=iso

(storage-attach-volume)=
### Attach the volume to an instance

After creating a custom storage volume, you can add it to one or more instances as a {ref}`disk device <devices-disk>`.

The following restrictions apply:

- Storage volumes of {ref}`content type <storage-content-types>` `block` or `iso` cannot be attached to containers, only to virtual machines.
- Storage volumes of {ref}`content type <storage-content-types>` `block` that don't have `security.shared` enabled cannot be attached to more than one instance at the same time.
  Attaching a `block` volume to more than one instance at a time risks data corruption.
- Storage volumes of {ref}`content type <storage-content-types>` `iso` are always read-only, and can therefore be attached to more than one virtual machine at a time without corrupting data.
- Storage volumes of {ref}`content type <storage-content-types>` `filesystem` can't be attached to virtual machines while they're running.

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
#### Configure I/O limits

When you attach a storage volume to an instance as a {ref}`disk device <devices-disk>`, you can configure I/O limits for it.
To do so, set the {config:option}`device-disk-device-conf:limits.read`, {config:option}`device-disk-device-conf:limits.write` or {config:option}`device-disk-device-conf:limits.max` properties to the corresponding limits.
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

(storage-volume-special)=
### Use the volume for backups or images

Instead of attaching a custom volume to an instance as a disk device, you can also use it as a special kind of volume to store {ref}`backups <backups>` or {ref}`images <about-images>`.

To do so, you must set the corresponding {ref}`server configuration <server-options-misc>`:

- To use a custom volume `my-backups-volume` to store the backup tarballs:

      lxc config set storage.backups_volume=my-pool/my-backups-volume

- To use a custom volume `my-images-volume` to store the image tarballs:

      lxc config set storage.images_volume=my-pool/my-images-volume

(storage-configure-volume)=
## Configure storage volume settings

See the {ref}`storage-drivers` documentation for a list of available storage volume configuration options for each driver.

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
