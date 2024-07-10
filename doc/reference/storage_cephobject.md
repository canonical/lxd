---
discourse: 14579,15457
relatedlinks: https://youtube.com/watch?v=kVLGbvRU98A
---

(storage-cephobject)=
# Ceph Object - `cephobject`

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph intro -->
    :end-before: <!-- Include end Ceph intro -->
```

[Ceph Object Gateway](https://docs.ceph.com/en/latest/radosgw/) is an object storage interface built on top of [`librados`](https://docs.ceph.com/en/latest/rados/api/librados-intro/) to provide applications with a RESTful gateway to [Ceph Storage Clusters](https://docs.ceph.com/en/latest/rados/).
It provides object storage functionality with an interface that is compatible with a large subset of the Amazon S3 RESTful API.

## Terminology

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph terminology -->
    :end-before: <!-- Include end Ceph terminology -->
```

A *Ceph Object Gateway* consists of several OSD pools and one or more *Ceph Object Gateway daemon* (`radosgw`) processes that provide object gateway functionality.

## `cephobject` driver in LXD

```{note}
The `cephobject` driver can only be used for buckets.

For storage volumes, use the {ref}`Ceph <storage-ceph>` or {ref}`CephFS <storage-cephfs>` drivers.
```

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph driver cluster -->
    :end-before: <!-- Include end Ceph driver cluster -->
```

You must set up a `radosgw` environment beforehand and ensure that its HTTP/HTTPS endpoint URL is reachable from the LXD server or servers.
See [Manual Deployment](https://docs.ceph.com/en/latest/install/manual-deployment/) for information on how to set up a Ceph cluster and [Ceph Object Gateway](https://docs.ceph.com/en/latest/radosgw/) on how to set up a `radosgw` environment.

The `radosgw` URL can be specified at pool creation time using the {config:option}`storage-cephobject-pool-conf:cephobject.radosgw.endpoint` option.

LXD uses the `radosgw-admin` command to manage buckets. So this command must be available and operational on the LXD servers.

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

## Configuration options

The following configuration options are available for storage pools that use the `cephobject` driver and for storage buckets in these pools.

(storage-cephobject-pool-config)=
### Storage pool configuration

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group storage-cephobject-pool-conf start -->
    :end-before: <!-- config group storage-cephobject-pool-conf end -->
```

### Storage bucket configuration

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group storage-cephobject-bucket-conf start -->
    :end-before: <!-- config group storage-cephobject-bucket-conf end -->
```
