(storage-dir)=
# Directory - `dir`

The directory storage driver is a basic backend that stores its data in a standard file and directory structure.
This driver is quick to set up and allows inspecting the files directly on the disk, which can be convenient for testing.
However, LXD operations are {ref}`not optimized <storage-drivers-features>` for this driver.

## `dir` driver in LXD

The `dir` driver in LXD is fully functional and provides the same set of features as other drivers.
However, it is much slower than all the other drivers because it must unpack images and do instant copies of instances, snapshots and images.

Unless specified differently during creation (with the `source` configuration option), the data is stored in the `/var/snap/lxd/common/lxd/storage-pools/` (for snap installations) or `/var/lib/lxd/storage-pools/` directory.

(storage-dir-quotas)=
### Quotas

The `dir` driver supports storage quotas when running on either ext4 or XFS with project quotas enabled at the file system level.

## Configuration options

The following configuration options are available for storage pools that use the `dir` driver and for storage volumes in these pools.

### Storage pool configuration
Key                           | Type                          | Default                                 | Description
:--                           | :---                          | :------                                 | :----------
rsync.bwlimit                 | string                        | 0 (no limit)                            | Specifies the upper limit to be placed on the socket I/O whenever rsync has to be used to transfer storage entities
rsync.compression             | bool                          | true                                    | Whether to use compression while migrating storage pools
source                        | string                        | -                                       | Path to block device or loop file or filesystem entry

### Storage volume configuration
Key                     | Type      | Condition                 | Default                               | Description
:--                     | :---      | :--------                 | :------                               | :----------
security.shifted        | bool      | custom volume             | false                                 | Enable id shifting overlay (allows attach by multiple isolated instances)
security.unmapped       | bool      | custom volume             | false                                 | Disable id mapping for the volume
size                    | string    | appropriate driver        | same as volume.size                   | Size of the storage volume
snapshots.expiry        | string    | custom volume             | -                                     | Controls when snapshots are to be deleted (expects expression like `1M 2H 3d 4w 5m 6y`)
snapshots.pattern       | string    | custom volume             | snap%d                                | Pongo2 template string which represents the snapshot name (used for scheduled snapshots and unnamed snapshots)
snapshots.schedule      | string    | custom volume             | -                                     | {{snapshot_schedule_format}}
