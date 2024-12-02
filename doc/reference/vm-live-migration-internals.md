---
discourse: 50734
---

(vm-live-migration-internals)=
# LXD VM Live Migration

LXD supports [live migration](https://documentation.ubuntu.com/lxd/en/latest/howto/move_instances/#live-migration) of virtual machines by streaming instance state from a source QEMU to a target QEMU. VM live migration is supported for all storage pool types.

Please see the [specification](https://discourse.ubuntu.com/t/online-vm-live-migration-qemu-to-qemu/50734) for further implementation details, design choices, and relevant pull requests.

API Extension: `migration_vm_live`

## Conceptual Process

The live migration workflow varies depending on the type of storage pool used. The two key scenarios are non-shared storage and shared storage within a cluster (e.g., Ceph). If live state transfer is not supported by a target, a stateful stop is performed prior to migration.

### Non-Shared Storage Live Migration

This process leverages the QEMU built-in Network Block Device (NBD) client-server mechanism to transfer the virtual machine's disk and memory state. Below is an overview of the steps:

1. Set QMP Capabilities.
2. Temporary Write Buffer: Create a temporary QCOW2 file to store writes that occur in the guest during the initial transfer of the root disk.
3. Snapshot Root Disk.
4. DB Record Creation: Create the target instance and volume DB records.
5. Storage Driver Transfer.
6. Start Target QEMU: Launch the QEMU process on the target.
7. Add NBD Node: Attach the NBD target disk as a block node on the source. 
8. Block Device Mirror: Perform a block device mirror of the snapshot to the target NBD server.
9. Pause Source VM: Pause the source VM once the block device mirror enters `ready` state.
10. State Transfer: Transfer the memory state over WebSocket.
11. Enter `pre-switchover` State.
12. Complete Migration: Continue state migration until `completed` state reached.

```{figure} /images/vm_live_migration_flowchart.svg
Non-Shared Storage Migration
```

The state transitions during the process are shown below:

```{figure} /images/vm_live_migration_fsa_diagram.svg
Non-Shared Storage Migration State Transitions
```

### Intra-Cluster Member Live Migration (Ceph Shared Storage Pool)

For shared storage pools such as Ceph, disk data transfer is unnecessary. Instead, the process focuses on transferring the VM state through a dedicated migration socket.

1. Validate cluster state and storage pool readiness.
2. Transfer the live state data over the migration socket.
3. Start the target VM using the transferred state.

## Implementation Details

### Migration API

Sending a `POST` request to `/1.0/instances/{name}` renames, moves an instance between pools or migrates an instance to another server. The returned operation metadata for migration, in the push case, will be a background operation with progress data, for the pull case, it will be a WebSocket operation with a number of secrets to be passed to the target server.

### Live Migration Call Stack

Below is a general overview of the key functions part of the live migration call stack:

#### [`lxd/lxd/instance_post.go`](https://github.com/canonical/lxd/lxd/instance_post.go)

1. [`instancePost`](https://github.com/canonical/lxd/lxd/instance_post.go#L74)

This function handles post requests to the `/1.0/instances` endpoint.

#### [`lxd/lxd/migrate_instance.go`](https://github.com/canonical/lxd/lxd/migrate_instance.go)

1. [`Do`](https://github.com/canonical/lxd/lxd/migrate_instance.go#L87)

This function performs the migration operation on the source VM for the given state and operation. It sets up the necessary WebSocket connections for control, state, and filesystem, and then initiates the migration process.

#### [`lxd/lxd/instance/drivers/driver_qemu.go`](https://github.com/canonical/lxd/lxd/instance/drivers/driver_qemu.go)

1. [`MigrateSend`](https://github.com/canonical/lxd/lxd/instance/drivers/driver_qemu.go#L6436)

This function controls the sending of a migration, checking for stateful support, waiting for connections, performing checks, and sending a migration offer. When performing a intra-cluster same-name migration, steps are taken to prevent corruption of volatile device configuration keys during start and stop of the instance on both source and target.

2. [`migrateSendLive`](https://github.com/canonical/lxd/lxd/instance/drivers/driver_qemu.go#L6666)

This function performs the live migration send process.

##### High-Level Steps

1. Setup connection.
2. Determine migration type (shared or non-shared storage).
3. Set migration capabilities.
4. Non-shared storage preparation.
- Create and configure snapshot file for root disk writes during migration;
- Add the snapshot as a block device to the source VM;
- Redirect disk writes to the snapshot.
5. Storage transfer.
- For shared storage, we just perform checks at this point;
- For non-shared storage, set up an NBD listener and connect it to the target to transfer disk.
6. Snapshot sync for non-shared storage using `BlockDevMirror`.
7. Transfer VM state to target.

## Appendix

### Glossary

QEMU: Emulation and virtualization software.

QCOW2: QEMU Copy On Write file format.

QMP: QEMU Machine Protocol, a JSON-based control protocol for QEMU.

NBD: Network Block Device, a protocol to access block devices over the network.

Snapshot: A point-in-time copy of storage data.
