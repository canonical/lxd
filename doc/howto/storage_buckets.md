---
relatedlinks: "[LXD's&#32;S3&#32;API&#32;-&#32;YouTube](https://youtube.com/watch?v=T1EeXPrjkEY)"
---

(howto-storage-buckets)=
# How to manage storage buckets

{ref}`storage-buckets` let you store and manage object-based data using non-local object storage.

Unlike custom storage volumes, storage buckets cannot be attached to instances. Instead, applications access them directly via a URL using the S3 protocol.

- A {ref}`Ceph RADOS Gateway endpoint <howto-storage-pools-ceph-requirements-radosgw-endpoint>` provides the S3-compatible URL.

(howto-storage-buckets-view)=
## View storage buckets

`````{tabs}
````{group-tab} CLI

To list all available storage buckets in a storage pool, run:

```bash
lxc storage bucket list <pool-name>
```

To show detailed information about a specific bucket, run:

```bash
lxc storage bucket show <pool-name> <bucket-name>
```

````
````{group-tab} UI

Select {guilabel}`Buckets` from the {guilabel}`Storage` section of the main navigation.

````
`````

(howto-storage-buckets-create)=
## Create a storage bucket

(howto-storage-buckets-create-requirements)=
### Requirements

Your LXD server must have access to a {ref}`Ceph Object <storage-cephobject>` storage pool.

`````{tabs}
````{group-tab} CLI
To view available storage pools, run:

```bash
lxc storage list
```

If you see a storage pool in the output with the `cephobject` driver, you're all set. Continue on to the instructions below to {ref}`create a storage bucket <howto-storage-buckets-create-single>`.

If you don't see a pool that uses a `cephobject` storage driver, you must create one before you can continue. This requires a [Ceph](https://ceph.io) cluster with a RADOS Gateway (`radosgw`) enabled. See our how-to guide for storage pools: {ref}`howto-storage-pools-ceph-requirements`.

````
````{group-tab} UI
To create a storage bucket, select {guilabel}`Buckets` from the {guilabel}`Storage` section of the main navigation.

On the resulting screen, click {guilabel}`Create bucket` in the upper-right corner.

In the form that appears, set a unique name for the storage bucket and select a storage pool. You can optionally configure the bucket's size and description.

Finally, click {guilabel}`Create bucket`.

```{figure} /images/storage/storage_buckets/storage_bucket_create.png
:width: 60%
:alt: Create Storage Buckets in LXD UI

```
````
`````

(howto-storage-buckets-create-single)=
### Create a bucket on a single, non-clustered LXD server

To create a storage bucket on a non-clustered LXD server, run:

```bash
lxc storage bucket create <pool-name> <bucket-name> [configuration_options...]
```

See the {ref}`Ceph Object <storage-cephobject>` documentation for a list of available storage bucket configuration options for the driver.

(howto-storage-buckets-create-cluster)=
### Create a bucket on a cluster member

#### Distributed storage buckets

Storage buckets created in `cephobject` storage pools are available from any LXD cluster member. Thus, to create this bucket, the command remains the same as for a non-clustered LXD server:

```bash
lxc storage bucket create <pool-name> <bucket-name> [configuration_options...]
```

(howto-storage-buckets-configure)=
## Configure storage bucket settings

See the {ref}`Ceph Object <storage-cephobject>` documentation for a list of available storage bucket configuration options for the driver.

`````{tabs}
````{group-tab} CLI
Use the following command to set configuration options for a storage bucket:

```bash
lxc storage bucket set <pool-name> <bucket-name> <key> <value>
```

For example, to set the size (quota) of a bucket, use the following command:

```bash
lxc storage bucket set my-pool my-bucket size 1MiB
```

You can also edit the storage bucket configuration by using the following command:

```bash
lxc storage bucket edit <pool-name> <bucket-name>
```

Use the following command to delete a storage bucket and its keys:

```bash
lxc storage bucket delete <pool-name> <bucket-name>
```

````
````{group-tab} UI

To configure a storage bucket, select {guilabel}`Buckets` from the {guilabel}`Storage` section of the main navigation.

The resulting screen shows a list of existing storage buckets. Click the {guilabel}`Edit` button on the row of the desired bucket to access its details.

After making changes, click the {guilabel}`Save changes` button. This button also displays the number of changes you have made.
````
`````

(howto-storage-buckets-resize)=
## Resize a storage bucket

By default, storage buckets do not have a quota applied.

`````{tabs}
````{group-tab} CLI
To set or change a quota for a storage bucket, set its size configuration:

```bash
lxc storage bucket set <pool-name> <bucket-name> size <new-size>
```

```{important}
- Growing a storage bucket usually works (if the storage pool has sufficient storage).
- You cannot shrink a storage bucket below its current used size.

```

````
````{group-tab} UI

To configure a storage bucket, select {guilabel}`Buckets` from the {guilabel}`Storage` section of the main navigation.

The resulting screen shows a list of existing storage buckets. Change the quota of the bucket by changing the values in the {guilabel}`Size` fields.

After making changes, click the {guilabel}`Save changes` button. This button also displays the number of changes you have made.
````
`````

(howto-storage-buckets-keys)=
## Manage storage bucket keys

To access a storage bucket, applications must use a set of S3 credentials made up of an *access key* and a *secret key*.
You can create multiple sets of credentials for a specific bucket.

Each set of credentials is given a key name.
The key name is used only for reference and does not need to be provided to the application that uses the credentials.

Each set of credentials has a *role* that specifies what operations they can perform on the bucket.

The roles available are:

- `admin` - Full access to the bucket
- `read-only` - Read-only access to the bucket (list and get files only)

If the role is not specified when creating a bucket key, the role used is `read-only`.

(howto-storage-buckets-keys-view)=
### View storage bucket keys

`````{tabs}
````{group-tab} CLI
Use the following command to see the keys defined for an existing bucket:

```
lxc storage bucket key list <pool-name> <bucket-name>
```

Use the following command to see a specific bucket key:

```
lxc storage bucket key show <pool-name> <bucket-name> <key-name>
```

````
````{group-tab} UI

To view storage bucket keys, select {guilabel}`Buckets` from the {guilabel}`Storage` section of the main navigation.

Click the name of a storage bucket to display its key management page, where you can view and manage a list of keys for that bucket.

```{figure} /images/storage/storage_buckets/storage_bucket_key_list.png
:width: 80%
:alt: List Storage Bucket keys in LXD UI
```
````
`````

(howto-storage-buckets-keys-create)=
### Create keys

`````{tabs}
````{group-tab} CLI
Use the following command to create a set of credentials for a storage bucket:

```bash
lxc storage bucket key create <pool-name> <bucket-name> <key-name> [configuration_options...]
```

Use the following command to create a set of credentials for a storage bucket with a specific role:

```bash
lxc storage bucket key create <pool-name> <bucket-name> <key-name> --role=admin [configuration_options...]
```

These commands will generate and display a random set of credential keys.

````
````{group-tab} UI

To create a storage bucket key, go to the {ref}`key management page <howto-storage-buckets-keys-view>` of the desired bucket.

On the resulting screen, click {guilabel}`Create key` in the upper-right corner.

In the form that appears, set a unique name for the key. You can optionally configure the role, description of your storage bucket key.

While you can enter values for the {guilabel}`Access` and {guilabel}`Secret Key` fields, this is not necessary. You can leave them blank, and LXD will generate random values for those credential keys.

Finally, click {guilabel}`Create key`.

```{figure} /images/storage/storage_buckets/storage_bucket_create_key.png
:width: 60%
:alt: Create Storage Bucket keys in LXD UI
```
````
`````

(howto-storage-buckets-keys-edit)=
### Edit or delete storage bucket keys

`````{tabs}
````{group-tab} CLI
Use the following command to edit an existing bucket key:

```bash
lxc storage bucket key edit <pool-name> <bucket-name> <key-name>
```

Use the following command to delete an existing bucket key:

```bash
lxc storage bucket key delete <pool-name> <bucket-name> <key-name>
```

````
````{group-tab} UI

To edit or delete storage bucket keys, go to the {ref}`key management page <howto-storage-buckets-keys-view>` of the desired bucket.

From here, use the {guilabel}`Edit` or {guilabel}`Delete` button in the row of the target key.
````
`````
