(storage-dir)=
# dir

 - While this backend is fully functional, it's also much slower than
   all the others due to it having to unpack images or do instant copies of
   instances, snapshots and images.
 - Quotas are supported with the directory backend when running on
   either ext4 or XFS with project quotas enabled at the filesystem level.

## Storage pool configuration
Key                           | Type                          | Default                                 | Description
:--                           | :---                          | :------                                 | :----------
rsync.bwlimit                 | string                        | 0 (no limit)                            | Specifies the upper limit to be placed on the socket I/O whenever rsync has to be used to transfer storage entities
rsync.compression             | bool                          | true                                    | Whether to use compression while migrating storage pools
source                        | string                        | -                                       | Path to block device or loop file or filesystem entry

## Storage volume configuration
Key                     | Type      | Condition                 | Default                               | Description
:--                     | :---      | :--------                 | :------                               | :----------
security.shifted        | bool      | custom volume             | false                                 | Enable id shifting overlay (allows attach by multiple isolated instances)
security.unmapped       | bool      | custom volume             | false                                 | Disable id mapping for the volume
size                    | string    | appropriate driver        | same as volume.size                   | Size of the storage volume
snapshots.expiry        | string    | custom volume             | -                                     | Controls when snapshots are to be deleted (expects expression like `1M 2H 3d 4w 5m 6y`)
snapshots.pattern       | string    | custom volume             | snap%d                                | Pongo2 template string which represents the snapshot name (used for scheduled snapshots and unnamed snapshots)
snapshots.schedule      | string    | custom volume             | -                                     | Cron expression (`<minute> <hour> <dom> <month> <dow>`), or a comma separated list of schedule aliases `<@hourly> <@daily> <@midnight> <@weekly> <@monthly> <@annually> <@yearly>`

## The following commands can be used to create directory storage pools

 - Create a new directory pool called "pool1".

```bash
lxc storage create pool1 dir
```

 - Use an existing directory for "pool2".

```bash
lxc storage create pool2 dir source=/data/lxd
```
