(storage-configure-volume)=
# How to configure storage volume settings

See the {ref}`storage-drivers` documentation for the available configuration options for each storage driver.

Use the following command to set configuration options for a storage volume:

    lxc storage volume set <pool_name> <volume_name> <key> <value>

For example, to set the snapshot expiry time to one month, use the following command:

    lxc storage volume set my-pool my-volume snapshots.expiry 1M

To configure an instance storage volume, specify the volume name including the {ref}`storage volume type <storage-volume-types>`, for example:

    lxc storage volume set my-pool container/my-container-volume user.XXX value

You can also edit the storage volume configuration by using the following command:

    lxc storage volume edit <pool_name> <volume_name>

(storage-configure-vol-default)=
## Configure default values for storage volumes

You can define default volume configurations for a storage pool.
To do so, set a storage pool configuration with a `volume` prefix, thus `volume.<VOLUME_CONFIGURATION>=<VALUE>`.

This value is then used for all new storage volumes in the pool, unless it is set explicitly for a volume or an instance.
In general, the defaults set on a storage pool level (before the volume was created) can be overridden through the volume configuration, and the volume configuration can be overridden through the instance configuration (for storage volumes of {ref}`type <storage-volume-types>` `container` or `vm`).

For example, to set a default volume size for a storage pool, use the following command:

    lxc storage set [<remote>:]<pool_name> volume.size <value>
