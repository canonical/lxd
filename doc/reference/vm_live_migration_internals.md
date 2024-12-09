---
discourse: ubuntu:[Online&#32;VM&#32;live-migration&#32;(QEMU&#32;to&#32;QEMU)](50734)
---

(vm-live-migration-internals)=
# LXD VM live migration

LXD supports {ref}`live-migration-vms` of virtual machines by streaming instance state from a source {abbr}`QEMU (Quick Emulator)` to a target QEMU. VM live migration is supported for all storage pool types.

API extension: `migration_vm_live`

## Conceptual process

The live migration workflow varies depending on the type of storage pool used. The two key scenarios are non-shared storage and shared storage within a cluster (e.g., Ceph). If live state transfer is not supported by a target, a stateful stop is performed prior to migration.

### Live migration for non-shared storage

This process leverages the QEMU built-in Network Block Device (NBD) client-server mechanism to transfer the virtual machine's disk and memory state. Below is an overview of the steps:

1. Set {abbr}`QMP (QEMU Machine Protocol)` capabilities.
2. Temporary write buffer: Create a temporary {abbr}`QCOW2 (QEMU Copy on Write)` file to store writes that occur in the guest during the initial transfer of the root disk.
3. Snapshot root disk.
4. Database record creation: Create the target instance and volume database records.
5. Storage driver transfer.
6. Start target QEMU: Launch the QEMU process on the target.
7. Add NBD node: Attach the NBD target disk as a block node on the source. 
8. Block device mirror: Perform a block device mirror of the snapshot to the target NBD server.
9. Pause source VM: Pause the source VM once the block device mirror enters `ready` state.
10. State transfer: Transfer the memory state over WebSocket.
11. Enter `pre-switchover` state.
12. Complete migration: Continue state migration until `completed` state reached.

```{figure} /images/vm_live_migration_flowchart.svg
Non-Shared Storage Migration
```

The state transitions during the process are shown below:

```{figure} /images/vm_live_migration_fsa_diagram.svg
Non-Shared Storage Migration State Transitions
```

### Intra-cluster member live migration (Ceph shared storage pool)

For shared storage pools such as Ceph, disk data transfer is unnecessary. Instead, the process focuses on transferring the VM state through a dedicated migration socket:

1. Validate cluster state and storage pool readiness.
2. Transfer the live state data over the migration socket.
3. Start the target VM using the transferred state.

## Implementation details

### Migration API

Sending a `POST` request to `/1.0/instances/{name}` renames, moves an instance between pools, or migrates an instance to another server. In the push case, the returned operation metadata for migration is a background operation with progress data. For the pull case, it is a WebSocket operation with a number of secrets to be passed to the target server.

### Live migration call stack

Below is a general overview of the key functions of the live migration call stack:

#### [`lxd/lxd/instance_post.go`](https://github.com/canonical/lxd/lxd/instance_post.go)

1. [`instancePost`](https://github.com/canonical/lxd/lxd/instance_post.go#L74)

This function handles post requests to the `/1.0/instances` endpoint.

#### [`lxd/lxd/migrate_instance.go`](https://github.com/canonical/lxd/lxd/migrate_instance.go)

1. [`Do`](https://github.com/canonical/lxd/lxd/migrate_instance.go#L87)

This function performs the migration operation on the source VM for the given state and operation. It sets up the necessary WebSocket connections for control, state, and filesystem, and then initiates the migration process.

#### [`lxd/lxd/instance/drivers/driver_qemu.go`](https://github.com/canonical/lxd/lxd/instance/drivers/driver_qemu.go)

1. [`MigrateSend`](https://github.com/canonical/lxd/lxd/instance/drivers/driver_qemu.go#L6436)

This function controls the sending of a migration, checking for stateful support, waiting for connections, performing checks, and sending a migration offer. When performing an intra-cluster same-name migration, steps are taken to prevent corruption of volatile device configuration keys during the start and stop of the instance on both source and target.

2. [`migrateSendLive`](https://github.com/canonical/lxd/lxd/instance/drivers/driver_qemu.go#L6666)

This function performs the live migration send process.

##### High-level steps

1. Set up connection.
2. Determine migration type (shared or non-shared storage).
3. Set migration capabilities.
4. Non-shared storage preparation.
- Create and configure snapshot file for root disk writes during migration;
- Add the snapshot as a block device to the source VM;
- Redirect disk writes to the snapshot.
5. Storage transfer.
- For shared storage, we just perform checks at this point;
- For non-shared storage, set up an NBD listener and connect it to the target to transfer the disk.
6. Snapshot sync for non-shared storage using `BlockDevMirror`.
7. Transfer VM state to target.
