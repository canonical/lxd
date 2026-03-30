---
relatedlinks: "[LXD's&#32;S3&#32;API&#32;-&#32;YouTube](https://youtube.com/watch?v=T1EeXPrjkEY)"
---

(howto-storage-buckets)=
# How to manage storage buckets

{ref}`storage-buckets` store object-based data using non-local `cephobject` storage pools. When used in LXD or MicroCloud clusters, they are available from any cluster member.

Unlike custom storage volumes, storage buckets cannot be attached to instances. Instead, applications access them directly via a URL using the S3 protocol. A {ref}`Ceph RADOS Gateway endpoint <howto-storage-pools-ceph-requirements-radosgw-endpoint>` provides the S3-compatible URL.

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

(howto-storage-buckets-requirements)=
## Requirements

To use storage buckets, your LXD server must have access to a storage pool that uses the {ref}`Ceph Object <storage-cephobject>` driver. You can confirm this by {ref}`viewing your available storage pools <howto-storage-pools-view>`.

If no listed pool uses the `cephobject` storage driver, you must create one. This requires a [Ceph](https://ceph.io) cluster with a RADOS Gateway (`radosgw`) enabled. Refer to our how-to guide for storage pools: {ref}`howto-storage-pools-ceph-requirements`.

(howto-storage-buckets-create)=
## Create a storage bucket

`````{tabs}
````{group-tab} CLI

To create a storage bucket, run:

```bash
lxc storage bucket create <pool-name> <bucket-name> [configuration_options...]
```

Refer to the {ref}`Ceph Object <storage-cephobject>` documentation for a list of available storage bucket configuration options for the driver.

````
````{group-tab} UI
To create a storage bucket, select {guilabel}`Buckets` from the {guilabel}`Storage` section of the main navigation.

On the resulting screen, click {guilabel}`Create bucket` in the upper-right corner.

In the form that appears, set a unique name for the storage bucket and select a storage pool. You can optionally configure the bucket's size and description.

````
`````

(howto-storage-buckets-configure)=
## Configure storage bucket settings

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

Refer to the {ref}`Ceph Object <storage-cephobject>` documentation for a list of available storage bucket configuration options for the driver.

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

````
````{group-tab} UI

To configure a storage bucket, select {guilabel}`Buckets` from the {guilabel}`Storage` section of the main navigation.

The resulting screen shows a list of existing storage buckets. Change the quota of the bucket by changing the values in the {guilabel}`Size` fields.

After making changes, click the {guilabel}`Save changes` button. This button also displays the number of changes you have made.
````
`````

```{admonition} Resizing considerations
:class: important
- Growing a storage bucket usually works (if the storage pool has sufficient storage).
- You cannot shrink a storage bucket below its current used size.
```

(howto-storage-buckets-keys)=
## Manage storage bucket keys

To access a storage bucket, applications must use a set of S3 credentials made up of an *access key* and a *secret key*. You can create multiple sets of credentials for a specific bucket.

Each set of credentials is given a key name. The key name is used only for reference and does not need to be provided to the application that uses the credentials.

Each set of credentials has a *role* that specifies what operations they can perform on the bucket. The available roles are:

`admin`
: Provides full access to the bucket.

`read-only`
: Default. Provides read-only (view) access to the bucket.

(howto-storage-buckets-keys-view)=
### View storage bucket keys

`````{tabs}
````{group-tab} CLI
Use the following command to list the keys defined for an existing bucket:

```
lxc storage bucket key list <pool-name> <bucket-name>
```

Use the following command to show a specific bucket key:

```
lxc storage bucket key show <pool-name> <bucket-name> <key-name>
```

````
````{group-tab} UI

To view storage bucket keys, select {guilabel}`Buckets` from the {guilabel}`Storage` section of the main navigation.

Click the name of a storage bucket to display its key management page, where you can view and manage a list of keys for that bucket.

````
`````

(howto-storage-buckets-keys-create)=
### Create keys

`````{tabs}
````{group-tab} CLI
Use the following command to generate and display a set of keys for a storage bucket. The default role is `read-only`. To create credentials with the `admin` role, include the `--role=admin` flag:

```bash
lxc storage bucket key create <pool-name> <bucket-name> <key-name> [--role=admin] [configuration_options...]
```

Refer to [`lxc storage bucket key create`](lxc_storage_bucket_key_create.md) for configuration options.

````
````{group-tab} UI

To create a storage bucket key, go to the {ref}`key management page <howto-storage-buckets-keys-view>` of the desired bucket. 

On the resulting screen, click {guilabel}`Create key` in the upper-right corner. In the form that appears, set a unique name for the key. You can optionally configure its role and description.

While you can enter values for the {guilabel}`Access` and {guilabel}`Secret Key` fields, this is not necessary. You can leave them blank, and LXD will generate random values for those credential keys.

````
`````

(howto-storage-buckets-keys-edit)=
### Edit or delete storage bucket keys

`````{tabs}
````{group-tab} CLI
To edit an existing bucket key, run:

```bash
lxc storage bucket key edit <pool-name> <bucket-name> <key-name>
```

To delete an existing bucket key, run:

```bash
lxc storage bucket key delete <pool-name> <bucket-name> <key-name>
```

````
````{group-tab} UI

You can edit or delete storage bucket keys from the {ref}`key management page <howto-storage-buckets-keys-view>` of the desired bucket.

````
`````

## Related topics

How-to guides:

- {ref}`howto-storage-pools-ceph-requirements`

Explanation:

- {ref}`storage-buckets`

Reference:

- {ref}`storage-cephobject`
- {ref}`storage-drivers-object`
