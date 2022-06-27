(storage-add-volume)=
# How to add a custom storage volume

When you create an instance, LXD automatically creates a storage volume that is used as the root disk for the instance.

You can add custom storage volumes to your instances.
Such custom storage volumes are independent of the instance, which means that they can be backed up separately and are retained until you delete them.
Custom storage volumes with content type `filesystem` can also be shared between different instances.

See {ref}`storage-volumes` for detailed information.

## Create a custom storage volume

Use the following command to create a custom storage volume in a storage pool:

    lxc storage volume create <pool_name> <volume_name> [configuration_options...]

See the {ref}`storage-drivers` documentation for a list of available storage volume configuration options for each driver.

By default, custom storage volumes use the `filesystem` {ref}`content type <storage-content-types>`.
To create a custom storage volume with the content type `block`, add the `--type` flag:

    lxc storage volume create <pool_name> <volume_name> --type=block [configuration_options...]

To add a custom storage volume on a cluster member, add the `--target` flag:

    lxc storage volume create <pool_name> <volume_name> --target=<cluster_member> [configuration_options...]

```{note}
For most storage drivers, custom storage volumes are not replicated across the cluster and exist only on the member for which they were created.
This behavior is different for Ceph-based storage pools (`ceph` and `cephfs`), where volumes are available from any cluster member.
```

(storage-attach-volume)=
## Attach a custom storage volume to an instance

After creating a custom storage volume, you can add it to one or more instances as a {ref}`disk device <instance_device_type_disk>`.

The following restrictions apply:

- Custom storage volumes of {ref}`content type <storage-content-types>` `block` cannot be attached to containers, but only to virtual machines.
- To avoid data corruption, storage volumes of {ref}`content type <storage-content-types>` `block` should never be attached to more than one virtual machine at a time.

For custom storage volumes with the content type `filesystem`, use the following command, where `<location>` is the path for accessing the storage volume inside the instance (for example, `/data`):

    lxc storage volume attach <pool_name> <filesystem_volume_name> <instance_name> <location>

Custom storage volumes with the content type `block` do not take a location:

    lxc storage volume attach <pool_name> <block_volume_name> <instance_name>

By default, the custom storage volume is added to the instance with the volume name as the {ref}`device <devices>` name.
If you want to use a different device name, you can add it to the command:

    lxc storage volume attach <pool_name> <filesystem_volume_name> <instance_name> <device_name> <location>
    lxc storage volume attach <pool_name> <block_volume_name> <instance_name> <device_name>

(storage-configure-IO)=
## Configure I/O limits

When you attach a storage volume to an instance as a {ref}`disk device <instance_device_type_disk>`, you can configure I/O limits for it.
To do so, set the `limits.read`, `limits.write` or `limits.max` properties to the corresponding limits (in IOp/s or MB/s).
See the {ref}`instance_device_type_disk` reference for more information.

The limits are applied through the Linux `blkio` cgroup controller, which makes it possible to restrict I/O at the disk level (but nothing finer grained than that).

```{note}
Because the limits apply to a whole physical disk rather than a partition or path, the following restrictions apply:

- Limits will not apply to filesystems that are backed by virtual devices (for example, device mapper).
- If a filesystem is backed by multiple block devices, each device will get the same limit.
- If two disk devices that are backed by the same disk are attached to the same instance, the limits of the two devices will be averaged.
```

All I/O limits only apply to actual block device access.
Therefore, consider the filesystem's own overhead when setting limits.
Access to cached data is not affected by the limit.
