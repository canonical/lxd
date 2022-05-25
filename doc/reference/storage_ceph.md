(storage-ceph)=
# Ceph

- Uses RBD images for images, then snapshots and clones to create instances
  and snapshots.
- Due to the way copy-on-write works in RBD, parent filesystems can't be
  removed until all children are gone. As a result, LXD will automatically
  prefix any removed but still referenced object with "zombie_" and keep it
  until such time the references are gone and it can safely be removed.
- Note that LXD will assume it has full control over the osd storage pool.
  It is recommended to not maintain any non-LXD owned filesystem entities in
  a LXD OSD storage pool since LXD might delete them.
- Note that sharing the same osd storage pool between multiple LXD instances is
  not supported. LXD only allows sharing of an OSD storage pool between
  multiple LXD instances only for backup purposes of existing instances via
  `lxd import`. In line with this, LXD requires the "ceph.osd.force_reuse"
  property to be set to true. If not set, LXD will refuse to reuse an osd
  storage pool it detected as being in use by another LXD instance.
- When setting up a Ceph cluster that LXD is going to use we recommend using
  `xfs` as the underlying filesystem for the storage entities that are used to
  hold OSD storage pools. Using `ext4` as the underlying filesystem for the
  storage entities is not recommended by Ceph upstream. You may see unexpected
  and erratic failures which are unrelated to LXD itself.
- To use Ceph osd pool of type "erasure" you __must__ have the osd pool created
  beforehand, as well as a separate osd pool of type "replicated" that will be used for
  storing metadata. This is required as RBD & CephFS do not support omap.
  To specify which pool is "earasure coded" you need to use the
  `ceph.osd.data_pool_name=<erasure-coded-pool-name>` and
  `source=<replicated-pool-name>` for the replicated pool.

## Storage pool configuration
Key                           | Type                          | Default                                 | Description
:--                           | :---                          | :------                                 | :----------
ceph.cluster\_name            | string                        | ceph                                    | Name of the Ceph cluster in which to create new storage pools
ceph.osd.data\_pool\_name     | string                        | -                                       | Name of the osd data pool
ceph.osd.force\_reuse         | bool                          | false                                   | Force using an osd storage pool that is already in use by another LXD instance
ceph.osd.pg\_num              | string                        | 32                                      | Number of placement groups for the osd storage pool
ceph.osd.pool\_name           | string                        | name of the pool                        | Name of the osd storage pool
ceph.rbd.clone\_copy          | bool                          | true                                    | Whether to use RBD lightweight clones rather than full dataset copies
ceph.rbd.du                   | bool                          | true                                    | Whether to use rbd du to obtain disk usage data for stopped instances.
ceph.rbd.features             | string                        | layering                                | Comma separate list of RBD features to enable on the volumes
ceph.user.name                | string                        | admin                                   | The Ceph user to use when creating storage pools and volumes
volatile.pool.pristine        | string                        | true                                    | Whether the pool has been empty on creation time

## Storage volume configuration
Key                     | Type      | Condition                 | Default                               | Description
:--                     | :---      | :--------                 | :------                               | :----------
block.filesystem        | string    | block based driver        | same as volume.block.filesystem       | Filesystem of the storage volume
block.mount\_options    | string    | block based driver        | same as volume.block.mount\_options   | Mount options for block devices
security.shifted        | bool      | custom volume             | false                                 | Enable id shifting overlay (allows attach by multiple isolated instances)
security.unmapped       | bool      | custom volume             | false                                 | Disable id mapping for the volume
size                    | string    | appropriate driver        | same as volume.size                   | Size of the storage volume
snapshots.expiry        | string    | custom volume             | -                                     | Controls when snapshots are to be deleted (expects expression like `1M 2H 3d 4w 5m 6y`)
snapshots.pattern       | string    | custom volume             | snap%d                                | Pongo2 template string which represents the snapshot name (used for scheduled snapshots and unnamed snapshots)
snapshots.schedule      | string    | custom volume             | -                                     | Cron expression (`<minute> <hour> <dom> <month> <dow>`), or a comma separated list of schedule aliases `<@hourly> <@daily> <@midnight> <@weekly> <@monthly> <@annually> <@yearly>`

## The following commands can be used to create Ceph storage pools

- Create a osd storage pool named "pool1" in the Ceph cluster "ceph".

```bash
lxc storage create pool1 ceph
```

- Create a osd storage pool named "pool1" in the Ceph cluster "my-cluster".

```bash
lxc storage create pool1 ceph ceph.cluster_name=my-cluster
```

- Create a osd storage pool named "pool1" with the on-disk name "my-osd".

```bash
lxc storage create pool1 ceph ceph.osd.pool_name=my-osd
```

- Use the existing osd storage pool "my-already-existing-osd".

```bash
lxc storage create pool1 ceph source=my-already-existing-osd
```

- Use the existing osd erasure coded pool "ecpool" and osd replicated pool "rpl-pool".

```bash
lxc storage create pool1 ceph source=rpl-pool ceph.osd.data_pool_name=ecpool
```
