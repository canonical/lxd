(storage-lvm)=
# LVM - `lvm`

 - Uses LVs for images, then LV snapshots for instances and instance snapshots.
 - The filesystem used for the LVs is ext4 (can be configured to use xfs instead).
 - By default, all LVM storage pools use an LVM thinpool in which logical
   volumes for all LXD storage entities (images, instances, etc.) are created.
   This behavior can be changed by setting "lvm.use\_thinpool" to "false". In
   this case, LXD will use normal logical volumes for all non-instance
   snapshot storage entities (images, instances, etc.). This means most storage
   operations will need to fallback to rsyncing since non-thinpool logical
   volumes do not support snapshots of snapshots. Note that this entails
   serious performance impacts for the LVM driver causing it to be close to the
   fallback DIR driver both in speed and storage usage. This option should only
   be chosen if the use-case renders it necessary.
 - For environments with high instance turn over (e.g continuous integration)
   it may be important to tweak the archival `retain_min` and `retain_days`
   settings in `/etc/lvm/lvm.conf` to avoid slowdowns when interacting with
   LXD.

## Storage pool configuration
Key                           | Type                          | Default                                 | Description
:--                           | :---                          | :------                                 | :----------
lvm.thinpool\_name            | string                        | LXDThinPool                             | Thin pool where volumes are created
lvm.thinpool\_metadata\_size  | string                        | 0 (auto)                                | The size of the thinpool metadata volume. The default is to let LVM calculate an appropriate size
lvm.use\_thinpool             | bool                          | true                                    | Whether the storage pool uses a thinpool for logical volumes
lvm.vg.force\_reuse           | bool                          | false                                   | Force using an existing non-empty volume group
lvm.vg\_name                  | string                        | name of the pool                        | Name of the volume group to create
rsync.bwlimit                 | string                        | 0 (no limit)                            | Specifies the upper limit to be placed on the socket I/O whenever rsync has to be used to transfer storage entities
rsync.compression             | bool                          | true                                    | Whether to use compression while migrating storage pools
source                        | string                        | -                                       | Path to block device or loop file or filesystem entry

## Storage volume configuration
Key                     | Type      | Condition                 | Default                               | Description
:--                     | :---      | :--------                 | :------                               | :----------
block.filesystem        | string    | block based driver        | same as volume.block.filesystem       | Filesystem of the storage volume
block.mount\_options    | string    | block based driver        | same as volume.block.mount\_options   | Mount options for block devices
lvm.stripes             | string    | LVM driver                | -                                     | Number of stripes to use for new volumes (or thin pool volume)
lvm.stripes.size        | string    | LVM driver                | -                                     | Size of stripes to use (at least 4096 bytes and multiple of 512bytes)
security.shifted        | bool      | custom volume             | false                                 | Enable id shifting overlay (allows attach by multiple isolated instances)
security.unmapped       | bool      | custom volume             | false                                 | Disable id mapping for the volume
size                    | string    | appropriate driver        | same as volume.size                   | Size of the storage volume
snapshots.expiry        | string    | custom volume             | -                                     | Controls when snapshots are to be deleted (expects expression like `1M 2H 3d 4w 5m 6y`)
snapshots.pattern       | string    | custom volume             | snap%d                                | Pongo2 template string which represents the snapshot name (used for scheduled snapshots and unnamed snapshots)
snapshots.schedule      | string    | custom volume             | -                                     | {{snapshot_schedule_format}}
