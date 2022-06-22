(storage-cephfs)=
# CephFS - `cephfs`

 - Can only be used for custom storage volumes
 - Supports snapshots if enabled on the server side

## Storage pool configuration
Key                           | Type                          | Default                                 | Description
:--                           | :---                          | :------                                 | :----------
cephfs.cluster\_name          | string                        | ceph                                    | Name of the Ceph cluster in which to create new storage pools
cephfs.fscache                | bool                          | false                                   | Enable use of kernel fscache and cachefilesd
cephfs.path                   | string                        | /                                       | The base path for the CephFS mount
cephfs.user.name              | string                        | admin                                   | The Ceph user to use when creating storage pools and volumes
source                        | string                        | -                                       | Existing storage pool or path in storage pool to use
volatile.pool.pristine        | string                        | true                                    | Whether the pool has been empty on creation time

## Storage volume configuration
Key                     | Type      | Condition                 | Default                               | Description
:--                     | :---      | :--------                 | :------                               | :----------
security.shifted        | bool      | custom volume             | false                                 | Enable id shifting overlay (allows attach by multiple isolated instances)
security.unmapped       | bool      | custom volume             | false                                 | Disable id mapping for the volume
size                    | string    | appropriate driver        | same as volume.size                   | Size of the storage volume
snapshots.expiry        | string    | custom volume             | -                                     | Controls when snapshots are to be deleted (expects expression like `1M 2H 3d 4w 5m 6y`)
snapshots.pattern       | string    | custom volume             | snap%d                                | Pongo2 template string which represents the snapshot name (used for scheduled snapshots and unnamed snapshots)
snapshots.schedule      | string    | custom volume             | -                                     | {{snapshot_schedule_format}}
