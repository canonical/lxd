# How to configure storage pool settings

See the {ref}`storage-drivers` documentation for the available configuration options for each storage driver.

General keys for a storage pool (like `source`) are top-level.
Driver-specific keys are namespaced by the driver name.

Use the following command to set configuration options for a storage pool:

    lxc storage set <pool_name> <key> <value>

For example, to turn off compression during storage pool migration for a `dir` storage pool, use the following command:

    lxc storage set my-dir-pool rsync.compression false

You can also edit the storage pool configuration by using the following command:

    lxc storage edit <pool_name>
