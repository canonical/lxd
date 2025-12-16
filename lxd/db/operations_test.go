//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
)

// Add, get and remove an operation.
func TestOperation(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	projectID, err := cluster.GetProjectID(context.Background(), tx.Tx(), "default")
	require.NoError(t, err)
	nodeID := tx.GetNodeID()
	uuid := "abcd"

	opInfo := cluster.Operation{
		NodeID:    nodeID,
		Type:      operationtype.InstanceCreate,
		Reference: uuid,
		ProjectID: &projectID,
	}

	id, err := cluster.CreateOrReplaceOperation(context.TODO(), tx.Tx(), opInfo)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	filter := cluster.OperationFilter{NodeID: &nodeID}
	operations, err := cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Len(t, operations, 1)
	assert.Equal(t, "abcd", operations[0].Reference)

	filter = cluster.OperationFilter{Reference: &uuid}
	ops, err := cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Len(t, ops, 1)
	operation := ops[0]
	assert.Equal(t, id, operation.ID)
	assert.Equal(t, operationtype.InstanceCreate, operation.Type)

	filter = cluster.OperationFilter{NodeID: &nodeID}
	ops, err = cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Equal(t, "abcd", ops[0].Reference)

	err = cluster.DeleteOperation(context.TODO(), tx.Tx(), "abcd")
	require.NoError(t, err)

	filter = cluster.OperationFilter{Reference: &uuid}
	ops, err = cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Empty(t, ops)
}

// Add, get and remove an operation not associated with any project.
func TestOperationNoProject(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID := tx.GetNodeID()
	uuid := "abcd"

	opInfo := cluster.Operation{
		NodeID:    nodeID,
		Type:      operationtype.InstanceCreate,
		Reference: uuid,
	}

	id, err := cluster.CreateOrReplaceOperation(context.TODO(), tx.Tx(), opInfo)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	filter := cluster.OperationFilter{NodeID: &nodeID}
	operations, err := cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Len(t, operations, 1)
	assert.Equal(t, "abcd", operations[0].Reference)

	filter = cluster.OperationFilter{Reference: &uuid}
	ops, err := cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Len(t, ops, 1)
	operation := ops[0]
	require.NoError(t, err)
	assert.Equal(t, id, operation.ID)
	assert.Equal(t, operationtype.InstanceCreate, operation.Type)

	filter = cluster.OperationFilter{NodeID: &nodeID}
	ops, err = cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Equal(t, "abcd", ops[0].Reference)

	err = cluster.DeleteOperation(context.TODO(), tx.Tx(), "abcd")
	require.NoError(t, err)

	filter = cluster.OperationFilter{Reference: &uuid}
	ops, err = cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Empty(t, ops)
}
