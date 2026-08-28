(howto-storage-block-tracking)=
# How to track changed blocks on virtual machine volumes

Changed block tracking records which blocks of a block volume the guest writes to after a given point in time, so that a backup tool copies only those blocks instead of the whole volume.
LXD implements it with named QEMU dirty bitmaps on the block volumes of a running virtual machine, and serves the volume data to an NBD client through the LXD API.

Bitmaps are transient.
They exist only in the QEMU process of the virtual machine, so any stop, reboot or migration of the virtual machine discards them, and the next backup must be a full one.

## Requirements

- The `storage_volume_block_tracking` API extension (see {ref}`extension-storage-volume-block-tracking`).
- A storage volume of {ref}`type <storage-volume-types>` `virtual-machine` or `custom` with {ref}`content type <storage-content-types>` `block`.
- For bitmaps and for reading a volume, the volume must be attached to exactly one running virtual machine.
  A `custom` volume that is attached to a container, to several instances, or to no instance at all is rejected.
- For writing a volume, every instance that uses the volume must be {ref}`stopped <instances-manage-stop>`.
- The `can_connect_nbd` entitlement on the instance or storage volume to read or write it over NBD, and the `can_edit` entitlement to create and delete bitmaps (see {ref}`permissions-reference`).
- An NBD client on the machine that runs the LXD client, for example `nbdinfo` and `nbdcopy` from `libnbd`, or `qemu-img`.

(storage-block-tracking-bitmaps)=
## Manage bitmaps

A bitmap starts recording guest writes to the volume when it is created and keeps recording until it is deleted.
Several bitmaps can exist on the same volume at the same time.

`````{tabs}
````{group-tab} CLI
Use the following commands to create, list, show and delete the bitmaps of a volume:

    lxc storage volume bitmap create <pool_name> [<volume_type>/]<volume_name> <bitmap_name>
    lxc storage volume bitmap list <pool_name> [<volume_type>/]<volume_name>
    lxc storage volume bitmap show <pool_name> [<volume_type>/]<volume_name> <bitmap_name>
    lxc storage volume bitmap delete <pool_name> [<volume_type>/]<volume_name> <bitmap_name>

The default volume type is `custom`, so the root volume of a virtual machine is referred to as `virtual-machine/<instance_name>`.

Showing a bitmap returns its name, the number of dirty bytes it has recorded, its granularity in bytes (the size of the block that one bit covers) and whether it is busy.

To create a bitmap of the same name on the root disk and on every non-shared block volume disk of a virtual machine, use the following command:

    lxc bitmap <instance_name> <bitmap_name>

All bitmaps are created in one QEMU transaction, so they start recording at the same instant.
Block volumes with `security.shared` enabled are skipped, because no single virtual machine sees every write to them.
````
````{group-tab} API
Send the following requests to create, list, show and delete the bitmaps of a volume:

    lxc query --request POST /1.0/storage-pools/<pool_name>/volumes/<volume_type>/<volume_name>/bitmaps --data '{"name": "<bitmap_name>"}'
    lxc query --request GET /1.0/storage-pools/<pool_name>/volumes/<volume_type>/<volume_name>/bitmaps?recursion=1
    lxc query --request GET /1.0/storage-pools/<pool_name>/volumes/<volume_type>/<volume_name>/bitmaps/<bitmap_name>
    lxc query --request DELETE /1.0/storage-pools/<pool_name>/volumes/<volume_type>/<volume_name>/bitmaps/<bitmap_name>

Showing a bitmap returns its name, the number of dirty bytes it has recorded, its granularity in bytes (the size of the block that one bit covers) and whether it is busy.

To create a bitmap of the same name on the root disk and on every non-shared block volume disk of a virtual machine, send the following request:

    lxc query --request POST /1.0/instances/<instance_name>/bitmaps --data '{"name": "<bitmap_name>"}'

All bitmaps are created in one QEMU transaction, so they start recording at the same instant.
Block volumes with `security.shared` enabled are skipped, because no single virtual machine sees every write to them.

See [`POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/bitmaps`](swagger:/storage/storage_pool_volumes_type_bitmaps_post) and [`POST /1.0/instances/{name}/bitmaps`](swagger:/instances/instance_bitmaps_post) for more information.
````
`````

(storage-block-tracking-read)=
## Read a volume while the virtual machine runs

The read-only NBD export presents the volume as it was at the moment the export was opened.
While the export is open, QEMU copies each block that the guest is about to overwrite into an overlay on the instance's config volume before the write lands, so the guest keeps running and the export stays consistent.

`````{tabs}
````{group-tab} CLI
Use the following command to serve a volume to a local NBD client:

    lxc storage volume nbd <pool_name> [<volume_type>/]<volume_name>

The command is not an NBD client.
It opens a local listener, prints the listening address, waits for one NBD client to connect, forwards that connection to LXD, and exits when the client disconnects:

    $ lxc storage volume nbd my-pool virtual-machine/my-vm
    NBD listening on 127.0.0.1:41337

Add the `--address` flag to choose where to listen instead of a random port on the loopback interface.

Each run serves exactly one client, so reading the changed block map and copying the data of the same frozen point in time needs either one client that does both over a single connection, or additional runs with `--reuse` while the first session is still open.

In a second terminal, point an NBD client at the printed address.
To list the blocks that a bitmap has recorded as changed, use the following command:

    nbdinfo --map=qemu:dirty-bitmap:<bitmap_name> nbd://127.0.0.1:41337

To copy the whole volume to a file, use the following command:

    nbdcopy --connections=1 nbd://127.0.0.1:41337 <file_path>

With `--reuse`, the command attaches to the session already open for the volume and keeps accepting clients while that session lasts, so that several clients read the same frozen point in time at once.
Without it, a second read session for any volume of the same virtual machine is rejected until the first one closes, because all read exports are served by the one NBD server of that QEMU process.
````
````{group-tab} API
Send a GET request with the `Upgrade: nbd` header to the NBD endpoint of the volume:

    GET /1.0/storage-pools/<pool_name>/volumes/<volume_type>/<volume_name>/nbd

The request body is optional and has the form `{"reuse": false}`.
LXD answers with `101 Switching Protocols`, after which the connection carries the NBD protocol and the client drives the export with NBD commands.
The export offers the `base:allocation` and `qemu:dirty-bitmap:<bitmap_name>` metadata contexts, so the client reads the allocation map and the changed block map of a bitmap over the same connection as the data.

With `reuse` set to `true`, the request attaches to the export that is already open for the volume instead of opening a new one, so that several clients read the same point in time.
Without it, a second read export for any volume of the same virtual machine is rejected until the first one closes.

See [`GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/nbd`](swagger:/storage/storage_pool_volumes_type_nbd_get) for more information.
````
`````

To take incremental backups, create a new bitmap before every backup, read the changed block map of the previous bitmap during the backup, and delete the previous bitmap once the backup is stored.
If a backup fails, keep the previous bitmap, because the blocks it recorded have not been backed up yet.
On the first backup there is no previous bitmap, so read the allocation map instead and copy the whole volume.

## Read all disks of a virtual machine

To read the root disk and every non-shared block volume disk of a virtual machine from the same point in time, serve the whole instance instead of one volume.

`````{tabs}
````{group-tab} CLI
Use the following command to serve every block disk of a virtual machine to a local NBD client:

    lxc nbd <instance_name>

The command prints the listening address in the same way as `lxc storage volume nbd`:

    $ lxc nbd my-vm
    NBD listening on 127.0.0.1:41337

Each disk is a separate NBD export named after its LXD disk device, so the client selects a disk by adding the device name to the URL:

    nbdinfo --map=qemu:dirty-bitmap:<bitmap_name> nbd://127.0.0.1:41337/root

The command takes the same `--address` and `--reuse` flags as `lxc storage volume nbd`.
````
````{group-tab} API
Send a GET request with the `Upgrade: nbd` header to the NBD endpoint of the instance:

    GET /1.0/instances/<instance_name>/nbd

The request body is optional and has the form `{"reuse": false}`.
Each disk is a separate NBD export named after its disk device, which the client selects during the NBD handshake.

See [`GET /1.0/instances/{name}/nbd`](swagger:/instances/instance_nbd_get) for more information.
````
`````

(storage-block-tracking-restore)=
## Write a volume while the virtual machine is stopped

To restore a backup, write it into the volume through a writable NBD export.
The virtual machine must be stopped, and for a `custom` volume every instance that uses the volume must be stopped.
To restore into a new virtual machine, first create it without an image:

    lxc init <instance_name> --empty --vm

`````{tabs}
````{group-tab} CLI
Use the following command to serve a volume writable to a local NBD client:

    lxc storage volume nbd <pool_name> [<volume_type>/]<volume_name> --writable

The command prints the listening address (for example, `NBD listening on 127.0.0.1:41337`).
Then write the backup into the export from a second terminal, with either of the following commands:

    qemu-img convert -n -f raw -O raw <file_path> nbd://127.0.0.1:41337
    nbdcopy <file_path> nbd://127.0.0.1:41337

The `--writable` flag cannot be combined with `--reuse`, because a writable export has no overlay and concurrent writers would reach the volume directly.
Start the virtual machine once the client has disconnected.
````
````{group-tab} API
Send a POST request with the `Upgrade: nbd` header to the NBD endpoint of the volume:

    POST /1.0/storage-pools/<pool_name>/volumes/<volume_type>/<volume_name>/nbd

LXD answers with `101 Switching Protocols`, after which the connection carries the NBD protocol and accepts writes.

See [`POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/nbd`](swagger:/storage/storage_pool_volumes_type_nbd_post) for more information.
````
`````

(storage-block-tracking-operations)=
## List and cancel NBD sessions

Every NBD session, whether a read-only export or a writable import, is represented by an operation on the cluster member that serves it.
The operation runs for as long as the client stays connected, and cancelling it closes the connection.

Use the following commands to list the open sessions and to end one:

    lxc operation list
    lxc operation delete <operation_id>

When the `101 Switching Protocols` response comes from the member that serves the session, its `Location` header carries the URL of the operation.

Creating and deleting bitmaps are operations as well.
The API requests return `202 Accepted` with the operation, and the CLI commands wait for it to complete.

## Limitations

- Bitmaps are transient.
  Any stop, reboot or migration of the virtual machine invalidates them, and the next backup must be a full one.
- Only one read session per virtual machine can be open at a time, unless additional clients attach to it with `--reuse`. Writable sessions lock per volume.
- The writable export requires the instance to be stopped, but LXD does not block starting the instance while the session is open.
  Do not start the instance before the client has disconnected.
- Reading a volume requires a running virtual machine.
  There is no read-only export of a stopped instance's volume.
