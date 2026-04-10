(cluster-recover-volumes)=
# How to recover orphaned volume database entries

When a {ref}`cluster instance migration <howto-cluster-manage-instance-migrate>` or {ref}`custom volume migration <howto-storage-move-volume-cluster>` is interrupted mid-transfer (for example, due to a network failure or a killed LXD process), the target member may be left with a volume record in the global database that has no corresponding storage on disk.
These orphaned entries block future migrations for the affected instance or custom volume with an error like:

    Volume "myinstance" exists in database on member "member2" but not on storage

## Recover instance volumes

### Identify orphaned entries

List all volume entries for the affected instance across cluster members:

    lxd sql global "SELECT storage_volumes.id, storage_volumes.name, nodes.name AS member FROM storage_volumes JOIN nodes ON storage_volumes.node_id = nodes.id WHERE storage_volumes.name = '<instance-name>'"

Replace `<instance-name>` with the name of the affected instance.

Orphaned entries appear as rows on a member where the instance does not actually reside and no storage exists on disk.

### Remove orphaned entries

Once you have identified the orphaned entry, remove it with:

    lxd sql global "DELETE FROM storage_volumes WHERE name='<instance-name>' AND node_id=(SELECT id FROM nodes WHERE name='<member-name>')"

Replace `<instance-name>`, as well as `<member-name>` with the cluster member that holds the orphaned entry.

After removing the orphaned entry, retry the {ref}`instance migration <howto-cluster-manage-instance-migrate>`.

## Recover custom volumes

The same issue can occur with custom storage volumes during migration.

### Identify orphaned entries

List all volume entries for the affected custom volume across cluster members:

    lxd sql global "SELECT storage_volumes.id, storage_volumes.name, nodes.name AS member FROM storage_volumes JOIN nodes ON storage_volumes.node_id = nodes.id WHERE storage_volumes.name = '<volume-name>'"

Replace `<volume-name>` with the name of the custom volume.

Orphaned entries appear as rows on a member where the volume does not actually reside and no storage exists on disk.

### Remove orphaned entries

Once you have identified the orphaned entry, remove it with:

    lxd sql global "DELETE FROM storage_volumes WHERE name='<volume-name>' AND node_id=(SELECT id FROM nodes WHERE name='<member-name>')"

Replace `<volume-name>`, as well as `<member-name>` with the cluster member that holds the orphaned entry.

After removing the orphaned entry, retry the {ref}`volume migration <howto-storage-move-volume-cluster>`.
