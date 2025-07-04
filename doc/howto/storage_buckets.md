---
relatedlinks: "[LXD's&#32;S3&#32;API&#32;-&#32;YouTube](https://youtube.com/watch?v=T1EeXPrjkEY)"
---

(howto-storage-buckets)=
# How to manage storage buckets

{ref}`storage-buckets` let you store and manage object-based data using either local or distributed storage.

Unlike custom storage volumes, storage buckets cannot be attached to instances. Instead, applications access them directly via a URL using the S3 protocol.

- For local buckets, the LXD server provides the S3-compatible URL via its {ref}`S3 address setting <howto-storage-buckets-create-requirements-local-s3>`.
- For distributed buckets, a {ref}`Ceph RADOS Gateway endpoint <howto-storage-pools-ceph-requirements-radosgw-endpoint>` provides the S3-compatible URL.

(howto-storage-buckets-view)=
## View storage buckets

To list all available storage buckets in a storage pool, run:

```bash
lxc storage bucket list <pool-name>
```

To show detailed information about a specific bucket, run:

```bash
lxc storage bucket show <pool-name> <bucket-name>
```

(howto-storage-buckets-create)=
## Create a storage bucket

(howto-storage-buckets-create-requirements)=
### Requirements

Storage buckets can only be created in storage pools that use a driver capable of **object storage**. View the {ref}`storage-buckets` reference guide's {ref}`storage-drivers-features` table to see which drivers support object storage.

Other requirements must be met before you can create a storage bucket, depending on whether you want to create a distributed or local storage bucket.

(howto-storage-buckets-create-requirements-distributed)=
#### Distributed storage buckets

To create a distributed storage bucket, your LXD server must have access to a {ref}`Ceph Object <storage-cephobject>` storage pool. To view available storage pools, run:

```bash
lxc storage list
```

If you see a storage pool in the output with the `cephobject` driver, you're all set. Continue on to the instructions below to {ref}`create a storage bucket <howto-storage-buckets-create-single>`.

If you don't see a pool that uses a `cephobject` storage driver, you must create one before you can continue. This requires a [Ceph](https://ceph.io) cluster with a RADOS Gateway (`radosgw`) enabled. See our how-to guide for storage pools: {ref}`howto-storage-pools-ceph-requirements`.

(howto-storage-buckets-create-requirements-local)=
#### Local storage buckets

(howto-storage-buckets-create-requirements-local-minio)=
##### MinIO

LXD uses [MinIO](https://min.io) to set up local storage buckets. To use this feature with LXD, you must install both the server and client binaries.

- MinIO Server:
   - Source:
      - [MinIO Server on GitHub](https://github.com/minio/minio)
   - Direct download for various architectures:
      - [MinIO Server pre-built for `amd64`](https://dl.min.io/server/minio/release/linux-amd64/minio)
      - [MinIO Server pre-built for `arm64`](https://dl.min.io/server/minio/release/linux-arm64/minio)
      - [MinIO Server pre-built for `arm`](https://dl.min.io/server/minio/release/linux-arm/minio)
      - [MinIO Server pre-built for `ppc64le`](https://dl.min.io/server/minio/release/linux-ppc64le/minio)
      - [MinIO Server pre-built for `s390x`](https://dl.min.io/server/minio/release/linux-s390x/minio)

- MinIO Client:
   - Source:
      - [MinIO Client on GitHub](https://github.com/minio/mc)
   - Direct download for various architectures:
      - [MinIO Client pre-built for `amd64`](https://dl.min.io/client/mc/release/linux-amd64/mc)
      - [MinIO Client pre-built for `arm64`](https://dl.min.io/client/mc/release/linux-arm64/mc)
      - [MinIO Client pre-built for `arm`](https://dl.min.io/client/mc/release/linux-arm/mc)
      - [MinIO Client pre-built for `ppc64le`](https://dl.min.io/client/mc/release/linux-ppc64le/mc)
      - [MinIO Client pre-built for `s390x`](https://dl.min.io/client/mc/release/linux-s390x/mc)

If LXD is installed from a Snap, you must configure the snap environment to detect the binaries, and restart LXD.
Note that the path to the directory containing the binaries must not be under the home directory of any user.

    snap set lxd minio.path=/path/to/directory/containing/both/binaries
    snap restart lxd

If LXD is installed from another source, both binaries must be included in the `$PATH` that LXD was started with.

(howto-storage-buckets-create-requirements-local-s3)=
##### Configure the S3 address

Storage buckets provide access to object storage exposed using the S3 protocol.

If you want to use storage buckets on local storage (thus in a `dir`, `btrfs`, `lvm`, or `zfs` pool), you must configure the S3 address for your LXD server.
This is the address that you can then use to access the buckets through the S3 protocol.

To configure the S3 address, set the {config:option}`server-core:core.storage_buckets_address` server configuration option.
For example:

    lxc config set core.storage_buckets_address :8555

(howto-storage-buckets-create-single)=
### Create a bucket on a single, non-clustered LXD server

Use the following command to create a storage bucket in a storage pool:

    lxc storage bucket create <pool-name> <bucket-name> [configuration_options...]

See the {ref}`storage-drivers` documentation for a list of available storage bucket configuration options for each driver that supports object storage.

(howto-storage-buckets-create-cluster)=
### Create a bucket on a cluster member

To add a storage bucket on a cluster member, add the `--target` flag:

    lxc storage bucket create <pool-name> <bucket-name> --target=<cluster-member> [configuration_options...]

```{note}
For most storage drivers, storage buckets are not replicated across the cluster and exist only on the member for which they were created.
This behavior is different for `cephobject` storage pools, where buckets are available from any cluster member.
```

(howto-storage-buckets-configure)=
## Configure storage bucket settings

See the {ref}`storage-drivers` documentation for the available configuration options for each storage driver that supports object storage.

Use the following command to set configuration options for a storage bucket:

    lxc storage bucket set <pool-name> <bucket-name> <key> <value>

For example, to set the size (quota) of a bucket, use the following command:

    lxc storage bucket set my-pool my-bucket size 1MiB

You can also edit the storage bucket configuration by using the following command:

    lxc storage bucket edit <pool-name> <bucket-name>

Use the following command to delete a storage bucket and its keys:

    lxc storage bucket delete <pool-name> <bucket-name>

(howto-storage-buckets-resize)=
## Resize a storage bucket

By default, storage buckets do not have a quota applied.

To set or change a quota for a storage bucket, set its size configuration:

    lxc storage bucket set <pool-name> <bucket-name> size <new-size>

```{important}
- Growing a storage bucket usually works (if the storage pool has sufficient storage).
- You cannot shrink a storage bucket below its current used size.

```

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

Use the following command to see the keys defined for an existing bucket:

    lxc storage bucket key list <pool-name> <bucket-name>

Use the following command to see a specific bucket key:

    lxc storage bucket key show <pool-name> <bucket-name> <key-name>

(howto-storage-buckets-keys-create)=
### Create keys

Use the following command to create a set of credentials for a storage bucket:

    lxc storage bucket key create <pool-name> <bucket-name> <key-name> [configuration_options...]

Use the following command to create a set of credentials for a storage bucket with a specific role:

    lxc storage bucket key create <pool-name> <bucket-name> <key-name> --role=admin [configuration_options...]

These commands will generate and display a random set of credential keys.

(howto-storage-buckets-keys-edit)=
### Edit or delete storage bucket keys

Use the following command to edit an existing bucket key:

    lxc storage bucket key edit <pool-name> <bucket-name> <key-name>

Use the following command to delete an existing bucket key:

    lxc storage bucket key delete <pool-name> <bucket-name> <key-name>

