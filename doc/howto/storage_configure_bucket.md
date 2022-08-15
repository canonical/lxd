# How to configure storage bucket settings

See the {ref}`storage-drivers` documentation for the available configuration options for each storage driver.

Use the following command to set configuration options for a storage bucket:

    lxc storage bucket set <pool_name> <bucket_name> <key> <value>

For example, to set the quota size of a bucket, use the following command:

    lxc storage bucket set my-pool my-bucket size 1MiB

You can also edit the storage bucket configuration by using the following command:

    lxc storage bucket edit <pool_name> <bucket_name>

Use the following command to delete a storage bucket and its keys:

    lxc storage bucket delete <pool_name> <bucket_name>

## How to configure storage bucket keys

Use the following command to edit an existing bucket key:

    lxc storage bucket edit <pool_name> <bucket_name> <key_name>

Use the following command to delete an existing bucket key:

    lxc storage bucket key delete <pool_name> <bucket_name> <key_name>
