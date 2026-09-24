(howto-storage-block-tracking)=
# How to track changed blocks on virtual machine volumes

Changed block tracking records the blocks of a block volume that a guest writes to after a snapshot.
This enables a backup tool to copy only those blocks instead of the whole volume.

LXD implements changed block tracking with QEMU dirty bitmaps.
LXD creates a bitmap together with an instance snapshot and names the bitmap after the snapshot.
When you take a later snapshot with a bitmap, LXD copies every bitmap of the volume into that snapshot.
LXD then serves the snapshot and its bitmap copies to an NBD client through the LXD API.

LXD keeps a bitmap across a stop, a reboot and a forced stop of the virtual machine.
LXD deletes a bitmap in the following cases:

- You delete or rename its snapshot.
- The QEMU process exits before LXD stores the bitmap, for example on a crash or a host power loss.
- Something other than the running virtual machine writes the volume, for example a restore or a read-write NBD export.

After LXD deletes a bitmap, the next backup must be a full one.

When you take the first snapshot with a bitmap of a block disk, LXD creates a small qcow2 image on the config volume of the virtual machine.
This image stores the bitmaps of the volume.
The guest reads and writes the volume directly.
LXD merges the bitmaps into the image before the QEMU process ends.
When the guest shuts down or reboots, the QEMU process pauses until LXD has merged the bitmaps.

## Requirements

Changed block tracking has the following requirements on the LXD server and client:

- Enable the `changed_block_tracking` feature preview on both the server and the client (see {ref}`howto-snap-configure-feature-previews`).
  While the preview is disabled, the server does not register the endpoints and does not advertise the `storage_volume_block_tracking` API extension (see {ref}`extension-storage-volume-block-tracking`).
  The server also rejects a snapshot with a bitmap, and the client hides the `lxc bitmap`, `lxc nbd` and `lxc storage volume nbd` commands.
- Install an NBD client on the machine that runs the LXD client.
  For example, use `nbdinfo` and `nbdcopy` from `libnbd`, or `qemu-img`.

LXD creates a bitmap on the root volume of the virtual machine.
If you take the snapshot with `--disk-volumes all-exclusive`, LXD also creates a bitmap on every attached `custom` volume of {ref}`content type <storage-content-types>` `block`.
LXD does not create a bitmap on a volume with `security.shared` enabled.
Several virtual machines can write to such a volume at once, but a bitmap records the writes of one virtual machine only.

Each action has its own requirements (see {ref}`permissions-reference` for the entitlements):

- To take a snapshot with a bitmap, start the virtual machine.
  You need the `can_manage_snapshots` entitlement on the instance.
- To list the bitmaps of a snapshot, you need the `can_view` entitlement on the instance.
- To read a snapshot over NBD, you need the `can_connect_nbd` entitlement on the instance.
- To write a volume over NBD, {ref}`stop <instances-manage-stop>` the virtual machine that uses the volume.
  You need the `can_connect_nbd` entitlement on the storage volume.

(storage-block-tracking-bitmaps)=
## Take a snapshot with a bitmap

LXD creates a bitmap together with an instance snapshot and names the bitmap after the snapshot.
The bitmap starts recording guest writes to the volume when you take the snapshot.
It continues recording until LXD deletes it.

LXD also copies every existing bitmap of the volume into the new snapshot.
Each copy records exactly the blocks that the guest wrote between the creation of its bitmap and the new snapshot.

`````{tabs}
````{group-tab} CLI
Use the following command to snapshot a virtual machine and create a bitmap on its root volume:

    lxc snapshot <instance_name> <snapshot_name> --bitmap

To also snapshot the attached block volumes and create a bitmap on each of them, add `--disk-volumes all-exclusive`.
````
````{group-tab} API
Set the `bitmap` field of the snapshot request:

    lxc query --request POST /1.0/instances/<instance_name>/snapshots --data '{"name": "<snapshot_name>", "bitmap": true}'

To also cover the attached block volumes, set `disk_volumes_mode` to `all-exclusive`.

See [`POST /1.0/instances/{name}/snapshots`](swagger:/instances/instance_snapshots_post) for more information.
````
`````

LXD rejects the request if the instance is not a running virtual machine.

Every bitmap has a name and a UUID.
The UUID of a bitmap is the UUID of its snapshot, which is the `volatile.uuid` of the root volume snapshot.
If you delete or rename a snapshot and then take a new snapshot with the same name, the new snapshot has a different UUID.

A backup tool stores the UUID together with the name.
It passes the UUID to the export of the next snapshot.
This ensures that the backup tool reads only the changes recorded by the bitmap it stored.

## List the bitmaps of a snapshot

`````{tabs}
````{group-tab} CLI
Use the following command to list the bitmap copies that an instance snapshot keeps:

    lxc bitmap list <instance_name>/<snapshot_name>

The command prints one row per bitmap and volume.
Each row shows the name and the UUID of the bitmap, the disk device, the pool, the type and the name of the volume, the granularity in bytes and whether the bitmap is recording writes.
The granularity is the size of the block that one bit covers.

Use the following command to show one bitmap with its name, its UUID and the volumes it exists on:

    lxc bitmap show <instance_name>/<snapshot_name> <bitmap_name>
````
````{group-tab} API
Send the following request to list the bitmap copies that an instance snapshot keeps:

    lxc query --request GET /1.0/instances/<instance_name>/snapshots/<snapshot_name>/bitmaps?recursion=1

Send the following request to show one bitmap:

    lxc query --request GET /1.0/instances/<instance_name>/snapshots/<snapshot_name>/bitmaps/<bitmap_name>

In both responses, each bitmap lists the volumes it exists on.
For each volume, the response shows the pool, the type, the name and the UUID of the volume, the disk device of the volume when you took the snapshot, the granularity in bytes and whether the bitmap is recording writes.

See [`GET /1.0/instances/{name}/snapshots/{snapshotName}/bitmaps`](swagger:/instances/instance_snapshot_bitmaps_get) for more information.
````
`````

The bitmap copies in a snapshot do not record writes, and LXD never modifies a snapshot.
Deleting a snapshot removes the bitmap with its name from every volume of the virtual machine.
Renaming a snapshot removes the bitmaps with its old name and its new name.
The other snapshots keep their bitmap copies.

(storage-block-tracking-read)=
## Read a snapshot

LXD serves an instance snapshot with a bitmap read-only over NBD.
The export publishes the bitmap copies of the snapshot as `qemu:dirty-bitmap:<bitmap_name>` metadata contexts, next to `base:allocation`.

You can limit the export to the bitmap copy of one snapshot by giving the UUID of that snapshot.
If no snapshot has that UUID, the export publishes no bitmap copy.
This happens, for example, when you delete a snapshot and take a new one with the same name.
In that case, the backup tool takes a full backup.

The virtual machine keeps running during the export.

`````{tabs}
````{group-tab} CLI
Use the following command to serve an instance snapshot to a local NBD client:

    lxc nbd <instance_name>/<snapshot_name>

The command opens a local listener and prints the listening address, for example:

    NBD listening on 127.0.0.1:41337

The command waits for one NBD client to connect and forwards that connection to LXD.
It exits when the client disconnects.
Use the `--address` flag to specify the listening address.
Without it, the command listens on a random port on the loopback interface.

Each run of the command serves one client, and each client opens its own session.
You can therefore run the command several times to serve the same snapshot to several clients.

LXD serves each volume snapshot as a separate NBD export, named after its disk device.
To select a volume, add the device name to the NBD URL.
In a second terminal, point an NBD client at the printed address.

To list the blocks that a bitmap recorded up to the snapshot, use the following command:

    nbdinfo --map=qemu:dirty-bitmap:<bitmap_name> nbd://127.0.0.1:41337/root

To copy the whole root volume snapshot to a file, use the following command:

    nbdcopy --connections=1 nbd://127.0.0.1:41337/root <file_path>

To serve only some of the volumes, add the `--devices` flag with a comma separated list of disk device names.
To serve only the bitmap copy of the previous snapshot, add the `--previous-snapshot-uuid` flag with the UUID of that snapshot.
````
````{group-tab} API
Send a GET request with the `Upgrade: nbd` header to the NBD endpoint of the instance snapshot:

    GET /1.0/instances/<instance_name>/snapshots/<snapshot_name>/nbd

LXD answers with `101 Switching Protocols`.
The client then sends NBD commands over the connection.

LXD serves each volume snapshot as a separate NBD export, named after its disk device.
The client selects an export during the NBD handshake.
To serve only some of the volumes, repeat the `device` query parameter once per disk device.
To serve only the bitmap copy of one snapshot, set the `previous_snapshot_uuid` query parameter to the UUID of that snapshot.

See [`GET /1.0/instances/{name}/snapshots/{snapshotName}/nbd`](swagger:/instances/instance_snapshot_nbd_get) for more information.
````
`````

To take incremental backups, take every snapshot with a bitmap.
Copy the first snapshot in full, because it has no copy of an earlier bitmap.
For every later snapshot, follow these steps:

1. Open the export of the new snapshot with the UUID of the previous snapshot.
1. Read the map of the bitmap named after the previous snapshot.
1. Copy the blocks that the map marks.
1. After you store the backup, delete the previous snapshot.

Deleting the previous snapshot removes its bitmap from the virtual machine.
The new snapshot keeps its own copy of that bitmap.

(storage-block-tracking-restore)=
## Write a volume while the virtual machine is stopped

To restore a backup, write it into the volume through a read-write NBD export.
Before you open the export, stop the virtual machine that uses the volume.
To restore into a new virtual machine, first create it without an image:

    lxc init <instance_name> --empty --vm

`````{tabs}
````{group-tab} CLI
Use the following command to serve a volume read-write to a local NBD client:

    lxc storage volume nbd <pool_name> [<volume_type>/]<volume_name> --writable

The default volume type is `custom`.
The command requires the `--writable` flag to confirm that you want to overwrite the volume.

The command prints the listening address, for example `NBD listening on 127.0.0.1:41337`.
It serves the volume under the default export.
In a second terminal, write the backup into the export with either of the following commands:

    qemu-img convert -n -f raw -O raw <file_path> nbd://127.0.0.1:41337
    nbdcopy <file_path> nbd://127.0.0.1:41337

After the client disconnects, start the virtual machine.
````
````{group-tab} API
Send a POST request with the `Upgrade: nbd` header to the NBD endpoint of the volume:

    POST /1.0/storage-pools/<pool_name>/volumes/<volume_type>/<volume_name>/nbd

LXD answers with `101 Switching Protocols`.
The client then sends NBD commands over the connection, including writes.

See [`POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/nbd`](swagger:/storage/storage_pool_volumes_type_nbd_post) for more information.
````
`````

The bitmaps of the volume do not record the writes of the export.
Therefore, LXD deletes them before the export starts.
The snapshots keep their bitmap copies.

(storage-block-tracking-operations)=
## List and cancel NBD sessions

LXD represents every NBD session with an operation on the cluster member that serves the session.
This applies to both snapshot exports and volume exports.
The operation runs while the client stays connected.
Cancelling the operation closes the connection.

Use the following command to list the open sessions:

    lxc operation list

Use the following command to end a session:

    lxc operation delete <operation_id>

If the member that serves the session sends the `101 Switching Protocols` response, the `Location` header of the response contains the URL of the operation.

## Limitations

The following events delete the bitmaps of a volume.
The copies kept by the snapshots are not affected, but the next snapshot records no bitmaps and the backup following it must be a full one.

- The QEMU process crashes, or the host loses power.
- The virtual machine is live migrated or moved to another cluster member.
- The volume is restored from a snapshot, copied, refreshed or imported.
- The volume is written through a read-write NBD export.
- The volume is resized.
- A custom volume is detached from the virtual machine.
- The `security.shared` is enabled on the volume.

Restoring an instance snapshot deletes the bitmaps of every volume of the virtual machine.

Bitmaps are named after the instance snapshots they were created with:

- Deleting a custom volume snapshot removes the bitmap created with it from that volume.
  The instance snapshot that recorded it then has no snapshot of that volume, and its export skips the volume.
- Deleting an instance snapshot removes the bitmap with its name from every volume.
- Renaming an instance snapshot removes the bitmaps with its old name and its new name.
- Renaming a custom volume snapshot does not affect the bitmaps.

While a snapshot with a bitmap is in progress:

- The virtual machine cannot be stopped or restarted.
- Its volumes cannot be resized or have `security.shared` enabled.
- A disk detach, and a power off or reboot from the guest, take effect when the snapshot ends.

While an NBD export is open:

- The virtual machine whose volume is exported cannot be started.
- The instance snapshot cannot be deleted or renamed.
- The custom volume snapshots that the exported instance snapshot recorded cannot be deleted or renamed.

After the first snapshot with a bitmap, if the guest powers off while LXD is not running, the virtual machine is reported as `STOPPING`.
It stays `STOPPING` until LXD starts and stops it.
