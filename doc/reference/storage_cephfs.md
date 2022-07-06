(storage-cephfs)=
# CephFS - `cephfs`

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

## `cephfs` driver in LXD

```{note}
The `cephfs` driver can only be used for custom storage volumes with content type `filesystem`.

For other storage volumes, use the {ref}`storage-ceph` driver.
That driver can also be used for custom storage volumes with content type `filesystem`, but it implements them through Ceph RBD images.
```

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph driver common -->
    :end-before: <!-- Include end Ceph driver common -->
```

The `cephfs` driver in LXD supports snapshots if snapshots are enabled on the server side.

## Configuration options

The following configuration options are available for storage pools that use the `cephfs` driver and for storage volumes in these pools.

### Storage pool configuration
Key                           | Type                          | Default                                 | Description
:--                           | :---                          | :------                                 | :----------
cephfs.cluster\_name          | string                        | ceph                                    | Name of the Ceph cluster in which to create new storage pools
cephfs.fscache                | bool                          | false                                   | Enable use of kernel fscache and cachefilesd
cephfs.path                   | string                        | /                                       | The base path for the CephFS mount
cephfs.user.name              | string                        | admin                                   | The Ceph user to use when creating storage pools and volumes
source                        | string                        | -                                       | Existing storage pool or path in storage pool to use
volatile.pool.pristine        | string                        | true                                    | Whether the pool has been empty on creation time

### Storage volume configuration
Key                     | Type      | Condition                 | Default                               | Description
:--                     | :---      | :--------                 | :------                               | :----------
security.shifted        | bool      | custom volume             | false                                 | {{enable_ID_shifting}}
security.unmapped       | bool      | custom volume             | false                                 | Disable ID mapping for the volume
size                    | string    | appropriate driver        | same as volume.size                   | Size/quota of the storage volume
snapshots.expiry        | string    | custom volume             | -                                     | {{snapshot_expiry_format}}
snapshots.pattern       | string    | custom volume             | snap%d                                | {{snapshot_pattern_format}}
snapshots.schedule      | string    | custom volume             | -                                     | {{snapshot_schedule_format}}
