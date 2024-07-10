//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db_test

import (
	"testing"

	"github.com/canonical/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Add, get and remove an operation.
func TestOperation(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	projectID, err := tx.GetProjectID("default")
	require.NoError(t, err)
	nodeID := tx.GetNodeID()
	uuid := "abcd"

	opInfo := db.Operation{
		NodeID:    nodeID,
		Type:      db.OperationInstanceCreate,
		UUID:      uuid,
		ProjectID: &projectID,
	}
	id, err := tx.CreateOrReplaceOperation(opInfo)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	filter := db.OperationFilter{NodeID: &nodeID}
	operations, err := tx.GetOperations(filter)
	require.NoError(t, err)
	assert.Len(t, operations, 1)
	assert.Equal(t, operations[0].UUID, "abcd")

	filter = db.OperationFilter{UUID: &uuid}
	ops, err := tx.GetOperations(filter)
	require.NoError(t, err)
	assert.Equal(t, len(ops), 1)
	operation := ops[0]
	assert.Equal(t, id, operation.ID)
	assert.Equal(t, db.OperationInstanceCreate, operation.Type)

	filter = db.OperationFilter{NodeID: &nodeID}
	ops, err = tx.GetOperations(filter)
	require.NoError(t, err)
	assert.Equal(t, "abcd", ops[0].UUID)

	err = tx.DeleteOperation("abcd")
	require.NoError(t, err)

	filter = db.OperationFilter{UUID: &uuid}
	ops, err = tx.GetOperations(filter)
	require.NoError(t, err)
	assert.Equal(t, len(ops), 0)
}

// Add, get and remove an operation not associated with any project.
func TestOperationNoProject(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID := tx.GetNodeID()
	uuid := "abcd"

	opInfo := db.Operation{
		NodeID: nodeID,
		Type:   db.OperationInstanceCreate,
		UUID:   uuid,
	}

	id, err := tx.CreateOrReplaceOperation(opInfo)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	filter := db.OperationFilter{NodeID: &nodeID}
	operations, err := tx.GetOperations(filter)
	require.NoError(t, err)
	assert.Len(t, operations, 1)
	assert.Equal(t, operations[0].UUID, "abcd")

	filter = db.OperationFilter{UUID: &uuid}
	ops, err := tx.GetOperations(filter)
	require.NoError(t, err)
	assert.Equal(t, len(ops), 1)
	operation := ops[0]
	require.NoError(t, err)
	assert.Equal(t, id, operation.ID)
	assert.Equal(t, db.OperationInstanceCreate, operation.Type)

	filter = db.OperationFilter{NodeID: &nodeID}
	ops, err = tx.GetOperations(filter)
	require.NoError(t, err)
	assert.Equal(t, "abcd", ops[0].UUID)

	err = tx.DeleteOperation("abcd")
	require.NoError(t, err)

	filter = db.OperationFilter{UUID: &uuid}
	ops, err = tx.GetOperations(filter)
	require.NoError(t, err)
	assert.Equal(t, len(ops), 0)
}
