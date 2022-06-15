# How to list storage pools and volumes

You can display a list of all available storage pools and the volumes in these pools and check their configuration.

## Show storage pool information

Use the following command to list all available storage pools:

    lxc storage list

The resulting table contains the storage pool that you created during initialization (usually called `default` or `local`) and any storage pools that you added.

To show detailed information about a specific pool, use the following command:

    lxc storage show <pool_name>

## Show storage volume information

To list all available storage volumes in a storage pool, use the following command:

    lxc storage volume list <pool_name>

The resulting table contains the {ref}`storage volume type <storage-volume-types>` and the {ref}`content type <storage-content-types>` for each storage volume in the pool.

To show detailed information about a specific volume, use the following command:

    lxc storage volume show <pool_name> <volume_name>
