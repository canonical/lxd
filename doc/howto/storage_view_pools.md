# How to view storage pools

You can display a list of all available storage pools and check their configuration.

Use the following command to list all available storage pools:

    lxc storage list

The resulting table contains the storage pool that you created during initialization (usually called `default` or `local`) and any storage pools that you added.

To show detailed information about a specific pool, use the following command:

    lxc storage show <pool_name>
