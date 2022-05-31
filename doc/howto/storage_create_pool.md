(storage_create_pool)=
# How to create a storage pool

LXD creates a storage pool during initialization.
You can add more storage pools later, using the same driver or different drivers.

To create a storage pool, use the following command:

    lxc storage create <pool_name> <driver> [configuration_options...]

See the {ref}`storage-drivers` documentation for a list of available configuration options for each driver.

## Examples

See the following examples for how to create a storage pool using different storage drivers.
