package db

import (
	"fmt"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/pkg/errors"
)

// Operation holds information about a single LXD operation running on a node
// in the cluster.
type Operation struct {
	ID          int64  // Stable database identifier
	UUID        string // User-visible identifier
	NodeAddress string // Address of the node the operation is running on
}

// OperationsUUIDs returns the UUIDs of all operations associated with this
// node.
func (c *ClusterTx) OperationsUUIDs() ([]string, error) {
	stmt := "SELECT uuid FROM operations WHERE node_id=?"
	return query.SelectStrings(c.tx, stmt, c.nodeID)
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
		return null, NoSuchObjectError
	case 1:
		return operations[0], nil
	default:
		return null, fmt.Errorf("more than one node matches")
	}
}

// OperationAdd adds a new operations to the table.
func (c *ClusterTx) OperationAdd(uuid string) (int64, error) {
	columns := []string{"uuid", "node_id"}
	values := []interface{}{uuid, c.nodeID}
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
		}
	}
	stmt := `
SELECT operations.id, uuid, nodes.address FROM operations JOIN nodes ON nodes.id = node_id `
	if where != "" {
		stmt += fmt.Sprintf("WHERE %s ", where)
	}
	stmt += "ORDER BY operations.id"
	err := query.SelectObjects(c.tx, dest, stmt, args...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fecth operations")
	}
	return operations, nil
}
