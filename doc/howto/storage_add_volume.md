(storage-add-volume)=
# How to add a storage volume

When you create an instance, LXD automatically creates a storage volume that is used as the root disk for the instance.

You can add custom storage volumes to your instances.
Such custom storage volumes are independent of the instance, which means that they can be backed up separately and are retained until you delete them.
Custom storage volumes with content type `filesystem` can also be shared between different instances.

See {ref}`storage-volumes` for detailed information.

## Create a storage volume

Use the following command to create a storage volume in a storage pool:

    lxc storage volume create <pool_name> <volume_name> [configuration_options...]

See the {ref}`storage-drivers` documentation for a list of available storage volume configuration options for each driver.

By default, storage volumes use the `filesystem` {ref}`content type <storage-content-types>`.
To create a storage volume with the content type `block`, add the `--type` flag:

    lxc storage volume create <pool_name> <volume_name> --type=block [configuration_options...]

To add a storage volume on a cluster member, add the `--target` flag:

    lxc storage volume create <pool_name> <volume_name> --target=<cluster_member> [configuration_options...]

```{note}
For most storage drivers, custom storage volumes are not replicated across the cluster and exist only on the member for which they were created.
This behavior is different for Ceph-based storage pools (`ceph` and `cephfs`), where volumes are available from any cluster member.
```

## Attach a storage volume to an instance

After creating a storage volume, you can add it to one or more instances as a {ref}`disk device <instance_device_type_nic>`.

The following restrictions apply:

- Storage volumes of {ref}`content type <storage-content-types>` `block` cannot be attached to containers, but only to virtual machines.
- To avoid data corruption, storage volumes of {ref}`content type <storage-content-types>` `block` should never be attached to more than one virtual machine at a time.

For storage volumes with the content type `filesystem`, use the following command, where `<location>` is the path for accessing the storage volume inside the instance (for example, `/data`):

    lxc storage volume attach <pool_name> <filesystem_volume_name> <instance_name> <location>

Storage volumes with the content type `block` do not take a location:

    lxc storage volume attach <pool_name> <block_volume_name> <instance_name>

By default, the storage volume is added to the instance with the volume name as the {ref}`device <devices>` name.
If you want to use a different device name, you can add it to the command:

    lxc storage volume attach <pool_name> <filesystem_volume_name> <instance_name> <device_name> <location>
    lxc storage volume attach <pool_name> <block_volume_name> <instance_name> <device_name>

## Configure I/O limits

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
