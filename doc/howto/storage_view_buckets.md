# How to view storage buckets

You can display a list of all available storage buckets in a storage pool and check their configuration.

To list all available storage buckets in a storage pool, use the following command:

    lxc storage bucket list <pool_name>

To show detailed information about a specific bucket, use the following command:

    lxc storage bucket show <pool_name> <bucket_name>

## How to view storage bucket keys

Use the following command to see the keys defined for an existing bucket:

    lxc storage bucket key list <pool_name> <bucket_name>

Use the following command to see a specific bucket key:

    lxc storage bucket key show <pool_name> <bucket_name> <key_name>

