---
discourse: 15457
---

(storage-cephfs)=
# CephFS - `cephfs`

```{youtube} https://youtube.com/watch?v=kVLGbvRU98A
```

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph intro -->
    :end-before: <!-- Include end Ceph intro -->
```

{abbr}`CephFS (Ceph File System)` is Ceph's file system component that provides a robust, fully-featured POSIX-compliant distributed file system.
Internally, it maps files to Ceph objects and stores file metadata (for example, file ownership, directory paths, access permissions) in a separate data pool.

## Terminology

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph terminology -->
    :end-before: <!-- Include end Ceph terminology -->
```

A *CephFS file system* consists of two OSD storage pools, one for the actual data and one for the file metadata.

## `cephfs` driver in LXD

```{note}
The `cephfs` driver can only be used for custom storage volumes with content type `filesystem`.

For other storage volumes, use the {ref}`Ceph <storage-ceph>` driver.
That driver can also be used for custom storage volumes with content type `filesystem`, but it implements them through Ceph RBD images.
```

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph driver cluster -->
    :end-before: <!-- Include end Ceph driver cluster -->
```

You can either create the CephFS file system that you want to use beforehand and specify it through the {config:option}`storage-cephfs-pool-conf:source` option, or specify the {config:option}`storage-cephfs-pool-conf:cephfs.create_missing` option to automatically create the file system and the data and metadata OSD pools (with the names given in {config:option}`storage-cephfs-pool-conf:cephfs.data_pool` and {config:option}`storage-cephfs-pool-conf:cephfs.meta_pool`).

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph driver remote -->
    :end-before: <!-- Include end Ceph driver remote -->
```

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph driver control -->
    :end-before: <!-- Include end Ceph driver control -->
```

The `cephfs` driver in LXD supports snapshots if snapshots are enabled on the server side.

## Configuration options

The following configuration options are available for storage pools that use the `cephfs` driver and for storage volumes in these pools.

(storage-cephfs-pool-config)=
### Storage pool configuration

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group storage-cephfs-pool-conf start -->
    :end-before: <!-- config group storage-cephfs-pool-conf end -->
```

{{volume_configuration}}

### Storage volume configuration

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group storage-cephfs-volume-conf start -->
    :end-before: <!-- config group storage-cephfs-volume-conf end -->
```
