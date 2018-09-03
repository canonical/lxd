package db

import (
	"fmt"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/pkg/errors"
)

// OperationType is a numeric code indentifying the type of an Operation.
type OperationType int64

// Possible values for OperationType
//
// WARNING: The type codes are stored in the database, so this list of
//          definitions should be normally append-only. Any other change
//          requires a database update.
const (
	OperationUnknown OperationType = iota
	OperationClusterBootstrap
	OperationClusterJoin
	OperationBackupCreate
	OperationBackupRename
	OperationBackupRestore
	OperationBackupRemove
	OperationConsoleShow
	OperationContainerCreate
	OperationContainerUpdate
	OperationContainerRename
	OperationContainerMigrate
	OperationContainerLiveMigrate
	OperationContainerFreeze
	OperationContainerUnfreeze
	OperationContainerDelete
	OperationContainerStart
	OperationContainerStop
	OperationContainerRestart
	OperationCommandExec
	OperationSnapshotCreate
	OperationSnapshotRename
	OperationSnapshotRestore
	OperationSnapshotTransfer
	OperationSnapshotUpdate
	OperationSnapshotDelete
	OperationImageDownload
	OperationImageDelete
	OperationImageToken
	OperationImageRefresh
	OperationVolumeCopy
	OperationVolumeCreate
	OperationVolumeMigrate
	OperationVolumeMove
	OperationProjectRename
)

// Description return a human-readable description of the operation type.
func (t OperationType) Description() string {
	switch t {
	case OperationClusterBootstrap:
		return "Creating bootstrap node"
	case OperationClusterJoin:
		return "Joining cluster"
	case OperationBackupCreate:
		return "Backing up container"
	case OperationBackupRename:
		return "Renaming container backup"
	case OperationBackupRestore:
		return "Restoring backup"
	case OperationBackupRemove:
		return "Removing container backup"
	case OperationConsoleShow:
		return "Showing console"
	case OperationContainerCreate:
		return "Creating container"
	case OperationContainerUpdate:
		return "Updating container"
	case OperationContainerRename:
		return "Renaming container"
	case OperationContainerMigrate:
		return "Migrating container"
	case OperationContainerLiveMigrate:
		return "Live-migrating container"
	case OperationContainerFreeze:
		return "Freezing container"
	case OperationContainerUnfreeze:
		return "Unfreezing container"
	case OperationContainerDelete:
		return "Deleting container"
	case OperationContainerStart:
		return "Starting container"
	case OperationContainerStop:
		return "Stopping container"
	case OperationContainerRestart:
		return "Restarting container"
	case OperationCommandExec:
		return "Executing command"
	case OperationSnapshotCreate:
		return "Snapshotting container"
	case OperationSnapshotRename:
		return "Renaming snapshot"
	case OperationSnapshotRestore:
		return "Restoring snapshot"
	case OperationSnapshotTransfer:
		return "Transferring snapshot"
	case OperationSnapshotUpdate:
		return "Updating snapshot"
	case OperationSnapshotDelete:
		return "Deleting snapshot"
	case OperationImageDownload:
		return "Downloading image"
	case OperationImageDelete:
		return "Deleting image"
	case OperationImageToken:
		return "Image download token"
	case OperationImageRefresh:
		return "Refreshing image"
	case OperationVolumeCopy:
		return "Copying storage volume"
	case OperationVolumeCreate:
		return "Creating storage volume"
	case OperationVolumeMigrate:
		return "Migrating storage volume"
	case OperationVolumeMove:
		return "Moving storage volume"
	case OperationProjectRename:
		return "Renaming project"
	default:
		return "Executing operation"

	}

}

// Operation holds information about a single LXD operation running on a node
// in the cluster.
type Operation struct {
	ID          int64         // Stable database identifier
	UUID        string        // User-visible identifier
	NodeAddress string        // Address of the node the operation is running on
	Type        OperationType // Type of the operation
}

// Operations returns all operations associated with this node.
func (c *ClusterTx) Operations() ([]Operation, error) {
	return c.operations("node_id=?", c.nodeID)
}

// OperationsUUIDs returns the UUIDs of all operations associated with this
// node.
func (c *ClusterTx) OperationsUUIDs() ([]string, error) {
	stmt := "SELECT uuid FROM operations WHERE node_id=?"
	return query.SelectStrings(c.tx, stmt, c.nodeID)
}

// OperationNodes returns a list of nodes that have running operations
func (c *ClusterTx) OperationNodes() ([]string, error) {
	stmt := "SELECT DISTINCT nodes.address FROM operations JOIN nodes ON nodes.id = node_id"
	return query.SelectStrings(c.tx, stmt)
}

// OperationByUUID returns the operation with the given UUID.
func (c *ClusterTx) OperationByUUID(uuid string) (Operation, error) {
	null := Operation{}
	operations, err := c.operations("uuid=?", uuid)
	if err != nil {
		return null, err
	}
	switch len(operations) {
	case 0:
		return null, ErrNoSuchObject
	case 1:
		return operations[0], nil
	default:
		return null, fmt.Errorf("more than one node matches")
	}
}

// OperationAdd adds a new operations to the table.
func (c *ClusterTx) OperationAdd(uuid string, typ OperationType) (int64, error) {
	columns := []string{"uuid", "node_id", "type"}
	values := []interface{}{uuid, c.nodeID, typ}
	return query.UpsertObject(c.tx, "operations", columns, values)
}

// OperationRemove removes the operation with the given UUID.
func (c *ClusterTx) OperationRemove(uuid string) error {
	result, err := c.tx.Exec("DELETE FROM operations WHERE uuid=?", uuid)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("query deleted %d rows instead of 1", n)
	}
	return nil
}

// Operations returns all operations in the cluster, filtered by the given clause.
func (c *ClusterTx) operations(where string, args ...interface{}) ([]Operation, error) {
	operations := []Operation{}
	dest := func(i int) []interface{} {
		operations = append(operations, Operation{})
		return []interface{}{
			&operations[i].ID,
			&operations[i].UUID,
			&operations[i].NodeAddress,
			&operations[i].Type,
		}
	}
	stmt := `
SELECT operations.id, uuid, nodes.address, type FROM operations JOIN nodes ON nodes.id = node_id `
	if where != "" {
		stmt += fmt.Sprintf("WHERE %s ", where)
	}
	stmt += "ORDER BY operations.id"
	err := query.SelectObjects(c.tx, dest, stmt, args...)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to fetch operations")
	}
	return operations, nil
}
