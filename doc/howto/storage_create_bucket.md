# How to create a storage bucket

Storage buckets provide object storage exposed using the S3 protocol.

Unlike custom storage volumes, storage buckets are not added to an instance, but instead are accessed directly via their URL by applications.

See {ref}`storage-buckets` for detailed information.

## Create a storage bucket

Use the following command to create a storage bucket in a storage pool:

    lxc storage bucket create <pool_name> <bucket_name> [configuration_options...]

See the {ref}`storage-drivers` documentation for a list of available storage bucket configuration options for each driver.

To add a storage bucket on a cluster member, add the `--target` flag:

    lxc storage bucket create <pool_name> <bucket_name> --target=<cluster_member> [configuration_options...]

```{note}
For most storage drivers, storage buckets are not replicated across the cluster and exist only on the member for which they were created.
This behavior is different for `cephobject` storage pools, where buckets are available from any cluster member.
```

## Create storage bucket keys

In order for an application to access a storage bucket they must use a set of S3 credentials made up of an *access key* and a *secret key*.
Multiple sets of credentials can be created for a specific bucket.

Each set of credentials is given a key name.
The key name is used only for reference and does not need to be provided to the application using the credentials.

Each set of credentials is given a *role* that specifies what operations they can perform on the bucket.

The roles available are:

 - `admin` - Full access to the bucket.
 - `read-only` - Read only access to the bucket (list and get files only).

If the role is not specified when creating a bucket key the role used is `read-only`.

Use the following command to create a set of credentials for a storage bucket:

    lxc storage bucket key create <pool_name> <bucket_name> <key_name> [configuration_options...]

Use the following command to create a set of credentials for a storage bucket with a specific role:

    lxc storage bucket key create <pool_name> <bucket_name> <key_name> --role=admin [configuration_options...]

These commands will generate and display a random set of credential keys.
