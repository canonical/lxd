(exp-storage)=
# Storage pools, volumes, and buckets

LXD stores its data in storage pools, divided into storage volumes of different content types (like images or instances).
You could think of a storage pool as the disk that is used to store data, while storage volumes are different partitions on this disk that are used for specific purposes.

In addition to storage volumes, there are storage buckets, which use the [Amazon {abbr}`S3 (Simple Storage Service)`](https://docs.aws.amazon.com/AmazonS3/latest/API/Welcome.html) protocol.
Like storage volumes, storage buckets are part of a storage pool.

(storage-pools)=
## Storage pools

During initialization, LXD prompts you to create a first storage pool.
If required, you can create additional storage pools later (see {ref}`storage-create-pool`).

Each storage pool uses a storage driver.
The following storage drivers are supported:

- [Directory - `dir`](storage-dir)
- [Btrfs - `btrfs`](storage-btrfs)
- [LVM - `lvm`](storage-lvm)
- [ZFS - `zfs`](storage-zfs)
- [Ceph RBD - `ceph`](storage-ceph)
- [CephFS - `cephfs`](storage-cephfs)
- [Ceph Object - `cephobject`](storage-cephobject)
- [Dell PowerFlex - `powerflex`](storage-powerflex)
- [Pure Storage - `pure`](storage-pure)

See the following how-to guides for additional information:

- {ref}`howto-storage-pools`
- {ref}`howto-storage-create-instance`

(storage-location)=
### Data storage location

Where the LXD data is stored depends on the configuration and the selected storage driver.
Depending on the storage driver that is used, LXD can either share the file system with its host or keep its data separate.

Storage location         | Directory | Btrfs    | LVM      | ZFS      | Ceph (all) | Dell PowerFlex | Pure Storage |
:---                     | :-:       | :-:      | :-:      | :-:      | :-:        | :-:            | :-:         |
Shared with the host     | &#x2713;  | &#x2713; | -        | &#x2713; | -          | -              | -           |
Dedicated disk/partition | -         | &#x2713; | &#x2713; | &#x2713; | -          | -              | -           |
Loop disk                | -         | &#x2713; | &#x2713; | &#x2713; | -          | -              | -           |
Remote storage           | -         | -        | -        | -        | &#x2713;   | &#x2713;       | &#x2713;    |

#### Shared with the host

Sharing the file system with the host is usually the most space-efficient way to run LXD.
In most cases, it is also the easiest to manage.

This option is supported for the `dir` driver, the `btrfs` driver (if the host is Btrfs and you point LXD to a dedicated sub-volume) and the `zfs` driver (if the host is ZFS and you point LXD to a dedicated dataset on your zpool).

#### Dedicated disk or partition

Having LXD use an empty partition on your main disk or a full dedicated disk keeps its storage completely independent from the host.

This option is supported  for the `btrfs` driver, the `lvm` driver and the `zfs` driver.

#### Loop disk

LXD can create a loop file on your main drive and have the selected storage driver use that.
This method is functionally similar to using a disk or partition, but it uses a large file on your main drive instead.
This means that every write must go through the storage driver and your main drive's file system, which leads to decreased performance.

The loop files reside in `/var/snap/lxd/common/lxd/disks/` if you are using the snap, or in `/var/lib/lxd/disks/` otherwise.

Loop files usually cannot be shrunk.
They will grow up to the configured limit, but deleting instances or images will not cause the file to shrink.
You can increase their size (quota) though; see {ref}`storage-resize-pool`.

#### Remote storage

The `ceph`, `cephfs` and `cephobject` drivers store the data in a completely independent Ceph storage cluster that must be set up separately.
The same applies to the `powerflex` and `pure` drivers.

(storage-default-pool)=
### Default storage pool

There is no concept of a default storage pool in LXD.

When you create a storage volume, you must specify the storage pool to use.

When LXD automatically creates a storage volume during instance creation, it uses the storage pool that is configured for the instance.
This configuration can be set in either of the following ways:

- Directly on an instance: [`lxc launch <image> <instance_name> --storage <storage_pool>`](lxc_launch.md)
- Through a profile: [`lxc profile device add <profile_name> root disk path=/ pool=<storage_pool>`](lxc_profile_device_add.md) and [`lxc launch <image> <instance_name> --profile <profile_name>`](lxc_launch.md)
- Through the default profile

In a profile, the storage pool to use is defined by the pool for the root disk device:

```yaml
  root:
    type: disk
    path: /
    pool: default
```

In the default profile, this pool is set to the storage pool that was created during initialization.

(storage-volumes)=
## Storage volumes

```{youtube} https://www.youtube.com/watch?v=dvQ111pbqtk
```

When you create an instance, LXD automatically creates the required storage volumes for it.
You can create additional storage volumes.

See the following how-to guides for additional information:

- {ref}`howto-storage-volumes`
- {ref}`howto-storage-move-volume`
- {ref}`howto-storage-backup-volume`

(storage-volume-types)=
### Storage volume types

Storage volumes can be of the following types:

`container`/`virtual-machine`
: LXD automatically creates one of these storage volumes when you launch an instance.
  It is used as the root disk for the instance and is destroyed when the instance is deleted.

  The storage pool can be explicitly specified by providing the `--storage` flag to the {ref}`launch command <lxc_launch.md>`.
  If no pool or profile is specified, LXD uses the storage pool of the default profile's root disk device.

`image`
: LXD automatically creates one of these storage volumes when it unpacks an image to launch one or more instances from it.
  You can delete it after the instance has been created.
  If you do not delete it manually, it is deleted automatically ten days after it was last used to launch an instance.

  The image storage volume is created in the same storage pool as the instance storage volume, and only for storage pools that use a {ref}`storage driver <storage-drivers>` that supports optimized image storage.

`custom`
: You can add one or more custom storage volumes to hold data that you want to store separately from your instances.
  Custom storage volumes of content type `filesystem` or `iso` can be shared between instances, and they are retained until you delete them.

  You can also use custom storage volumes to hold your backups or images.

  You must specify the storage pool for the custom volume when you create it.

(storage-content-types)=
### Content types

Each storage volume uses one of the following content types:

`filesystem`
: This content type is used for containers and container images.
  It is the default content type for custom storage volumes.

  Custom storage volumes of content type `filesystem` can be attached to both containers and virtual machines, and they can be shared between instances.

`block`
: This content type is used for virtual machines and virtual machine images.
  You can create a custom storage volume of type `block` by using the `--type=block` flag.

  Custom storage volumes of content type `block` can only be attached to virtual machines.
  By default, they can only be attached to one instance at a time, because simultaneous access can lead to data corruption.
  Sharing custom storage volumes of content type `block` is made possible through the usage of the `security.shared` configuration key.

`iso`
: This content type is used for custom ISO volumes.
  A custom storage volume of type `iso` can only be created by importing an ISO file using [`lxc storage volume import`](lxc_storage_volume_import.md) or by copying another volume.

  Custom storage volumes of content type `iso` can only be attached to virtual machines.
  They can be attached to multiple machines simultaneously as they are always read-only.

(storage-buckets)=
## Storage buckets

```{youtube} https://www.youtube.com/watch?v=T1EeXPrjkEY
```

Storage buckets provide object storage functionality via the S3 protocol.

They can be used in a way that is similar to custom storage volumes.
However, unlike storage volumes, storage buckets are not attached to an instance.
Instead, applications can access a storage bucket directly using its URL.

Each storage bucket is assigned one or more access keys, which the applications must use to access it.

Storage buckets can be located on local storage (with `dir`, `btrfs`, `lvm` or `zfs` pools) or on distributed storage (with {ref}`Ceph Object <storage-cephobject>` pools). For Ceph Object storage buckets, the {ref}`RADOS Gateway <howto-storage-pools-ceph-requirements-radosgw>` configured on the Ceph cluster acts as an S3 interface.

See the following how-to guide for additional information:

- {ref}`howto-storage-buckets`

## Related topics

{{storage_how}}

{{storage_ref}}
