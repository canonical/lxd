---
discourse: "[Online&#32;VM&#32;live-migration&#32;(QEMU&#32;to&#32;QEMU)](50734)"
---

(vm-live-migration-internals)=
# VM live migration implementation

{ref}`live-migration` in LXD is achieved by streaming instance state from a source {abbr}`QEMU (Quick Emulator)` to a target QEMU. VM live migration is supported for all storage pool types.

API extension: `migration_vm_live`

## Conceptual process

The live migration workflow varies depending on the type of storage pool used. The two key scenarios are non-shared storage and shared storage within a cluster (e.g., Ceph). If live state transfer is not supported by a target, a stateful stop is performed prior to migration.

### Live migration for non-shared storage

This process leverages the QEMU built-in Network Block Device (NBD) client-server mechanism to transfer the virtual machine's disk and memory state. Below is an overview of the steps:

1. Set up connection.
1. Determine migration type (shared or non-shared storage).
1. Set migration capabilities.
1. Non-shared storage preparation.
    1. Create and configure snapshot file for root disk writes during migration.
    1. Add the snapshot as a block device to the source VM.
    1. Redirect disk writes to the snapshot.
1. Storage transfer.
    1. For shared storage, we just perform checks at this point.
    1. For non-shared storage, set up an NBD listener and connect it to the target to transfer the disk.
1. Snapshot sync for non-shared storage to ensure consistency between source and target.
1. Transfer VM state to target.

```{figure} /images/vm_live_migration_flowchart.svg
Non-Shared Storage Migration
```

The state transitions during the process are shown below:

```{figure} /images/vm_live_migration_state_diagram.svg
Non-Shared Storage Migration State Transitions
```

### Intra-cluster member live migration (Ceph shared storage pool)

For shared storage pools such as Ceph, disk data transfer is unnecessary. Instead, the process focuses on transferring the VM state through a dedicated migration socket:

1. Validate cluster state and storage pool readiness.
1. Notify the shared disks that they will be accessed from another system.
1. Pause the guest OS on the source VM and transfer the live state data over the migration socket.
1. Stop and delete source VM.
1. Start the target VM using the transferred state.

## Migration API

Sending a `POST` request to `/1.0/instances/{name}` renames, moves an instance between pools, or migrates an instance to another server. In the push case, the returned operation metadata for migration is a background operation with progress data. For the pull case, it is a WebSocket operation with a number of secrets to be passed to the target server.

## Live migration call stack

Below is a general overview of the key functions of the live migration call stack:

### [`lxd/lxd/instance_post.go`](https://github.com/canonical/lxd/blob/main/lxd/instance_post.go)

[`instancePost`](https://github.com/canonical/lxd/blob/main/lxd/instance_post.go#L74)

This function handles post requests to the `/1.0/instances` endpoint.

### [`lxd/lxd/migrate_instance.go`](https://github.com/canonical/lxd/blob/main/lxd/migrate_instance.go)

[`Do`](https://github.com/canonical/lxd/blob/main/lxd/migrate_instance.go#L87)

This function performs the migration operation on the source VM for the given state and operation. It sets up the necessary WebSocket connections for control, state, and filesystem, and then initiates the migration process.

### [`lxd/lxd/instance/drivers/driver_qemu.go`](https://github.com/canonical/lxd/blob/main/lxd/instance/drivers/driver_qemu.go)

[`MigrateSend`](https://github.com/canonical/lxd/blob/main/lxd/instance/drivers/driver_qemu.go#L6436)

This function controls the sending of a migration, checking for stateful support, waiting for connections, performing checks, and sending a migration offer. When performing an intra-cluster same-name migration, steps are taken to prevent corruption of volatile device configuration keys during the start and stop of the instance on both source and target.

[`migrateSendLive`](https://github.com/canonical/lxd/blob/main/lxd/instance/drivers/driver_qemu.go#L6666)

This function performs the live migration send process:

1. Connect to the QEMU monitor: The function begins by establishing a connection to the QEMU monitor using `qmp.Connect`.
1. Define disk names: The function defines names for the root disk (`lxd_root`), the NBD target disk (`lxd_root_nbd`), and the snapshot disk (`lxd_root_snapshot`). These will be used later to manage the root disk and its snapshot during migration.
1. Check for shared storage: If the migration involves shared storage, the migration process can bypass the need for synchronizing the root disk. The function checks for this condition by verifying if `clusterMoveSourceName` is non-empty and the pool is remote.
1. Non-shared storage snapshot setup: If shared storage is not used, the function proceeds to set up a temporary snapshot of the root disk.
    1. Migration capabilities such as `auto-converge`, `pause-before-switchover`, and `zero-blocks` are set to optimize the migration process.
    1. The function creates a QCOW2 snapshot file of the root disk, which will store changes to the disk during migration.
    1. The snapshot file is opened for reading and writing, and the file descriptor is passed to QEMU.
    1. The snapshot is added as a block device to QEMU, ensuring that it is not visible to the guest OS.
    1. A snapshot of the root disk is taken using `monitor.BlockDevSnapshot`. This ensures that changes to the root disk are isolated during migration.
    1. Revert function: The revert function is used to clean up in case of failure. It ensures that the guest is resumed, and any changes made during snapshot creation are merged back into the root disk if migration fails
1. Shared storage setup: If shared storage is used, only the `auto-converge` migration capability is set, and no snapshot creation is necessary.
1. Perform storage transfer: The storage pool is migrated while the VM is still running. The `volSourceArgs.AllowInconsistent` flag is set to true to allow migration while the disk is in use. The migration checks are done by calling `pool.MigrateInstance`.
1. Notify shared disk pools: For each disk in the VM, the migration process checks if the disk belongs to a shared pool. If so, the disk is prepared for migration by calling `MigrateVolume` on the source disk.
1. Set up NBD listener and connection: If shared storage is not used, the function sets up a Unix socket listener for NBD connections. This listener handles the actual data transfer of the root disk from the source VM to the migration target.
1. Begin block device mirroring: After setting up the NBD connection, the function starts transferring the migration snapshot to the target disk by using `monitor.BlockDevMirror`.
1. Send stateful migration checkpoint: The function creates a pipe to transfer the state of the VM during the migration process. It writes the VMs state to the `stateConn` via the pipe, using `d.saveStateHandle` to handle the state transfer. Note that the source VMs guest OS is paused while the state is transferred. This ensures that the VMs state is consistent when the migration completes.
1. Finalize snapshot transfer: If non-shared storage is used, the function waits for the state transfer to reach the `pre-switchover` stage, ensuring that the guest remains paused during this process. Next, the function cancels the block job associated with the root snapshot to finalize the transfer and ensure that no changes are lost.
1. Completion: Once all transfers are complete, the function proceeds to finalize the migration process by resuming the target VM and ensuring that source VM resources are cleaned up. The source VM is stopped, and its storage is discarded.
