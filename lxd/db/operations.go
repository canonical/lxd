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
	OperationVolumeSnapshotCreate
	OperationVolumeSnapshotDelete
	OperationVolumeSnapshotUpdate
	OperationProjectRename
	OperationImagesExpire
	OperationImagesPruneLeftover
	OperationImagesUpdate
	OperationLogsExpire
	OperationInstanceTypesUpdate
	OperationBackupsExpire
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
	case OperationVolumeSnapshotCreate:
		return "Creating storage volume snapshot"
	case OperationVolumeSnapshotDelete:
		return "Deleting storage volume snapshot"
	case OperationVolumeSnapshotUpdate:
		return "Updating storage volume snapshot"
	case OperationProjectRename:
		return "Renaming project"
	case OperationImagesExpire:
		return "Cleaning up expired images"
	case OperationImagesPruneLeftover:
		return "Pruning leftover image files"
	case OperationImagesUpdate:
		return "Updating images"
	case OperationLogsExpire:
		return "Expiring log files"
	case OperationInstanceTypesUpdate:
		return "Updating instance types"
	case OperationBackupsExpire:
		return "Cleaning up expired backups"
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
func (c *ClusterTx) OperationNodes(project string) ([]string, error) {
	stmt := `
SELECT DISTINCT nodes.address
  FROM operations
  LEFT OUTER JOIN projects ON projects.id = operations.project_id
  JOIN nodes ON nodes.id = operations.node_id
 WHERE projects.name = ? OR operations.project_id IS NULL
`
	return query.SelectStrings(c.tx, stmt, project)
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
func (c *ClusterTx) OperationAdd(project, uuid string, typ OperationType) (int64, error) {
	var projectID interface{}

	if project != "" {
		var err error
		projectID, err = c.ProjectID(project)
		if err != nil {
			return -1, errors.Wrap(err, "Fetch project ID")
		}
	} else {
		projectID = nil
	}

	columns := []string{"uuid", "node_id", "type", "project_id"}
	values := []interface{}{uuid, c.nodeID, typ, projectID}
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
	sql := `
SELECT operations.id, uuid, nodes.address, type FROM operations JOIN nodes ON nodes.id = node_id `
	if where != "" {
		sql += fmt.Sprintf("WHERE %s ", where)
	}
	sql += "ORDER BY operations.id"
	stmt, err := c.tx.Prepare(sql)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	err = query.SelectObjects(stmt, dest, args...)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to fetch operations")
	}
	return operations, nil
}
