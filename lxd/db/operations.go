//go:build linux && cgo && !agent

package db

import (
	"context"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
)

// GetOperationsOfType returns a list operations that belong to the specified project and have the desired type.
func (c *ClusterTx) GetOperationsOfType(ctx context.Context, projectName string, opType operationtype.Type) ([]cluster.Operation, error) {
	var ops []cluster.Operation

	stmt := `
SELECT operations.id, operations.uuid, operations.type, nodes.address
  FROM operations
  LEFT JOIN projects on projects.id = operations.project_id
  JOIN nodes on nodes.id = operations.node_id
WHERE (projects.name = ? OR operations.project_id IS NULL) and operations.type = ?
`
	rows, err := c.tx.QueryContext(ctx, stmt, projectName, opType)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var op cluster.Operation
		err := rows.Scan(&op.ID, &op.UUID, &op.Type, &op.NodeAddress)
		if err != nil {
			return nil, err
		}

		ops = append(ops, op)
	}

	if rows.Err() != nil {
		return nil, err
	}

	return ops, nil
}
