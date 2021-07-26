//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Add, get and remove an operation.
func TestOperation(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	projectID, err := tx.GetProjectID("default")
	require.NoError(t, err)

	opInfo := db.Operation{
		NodeID:    *tx.GetNodeID(),
		Type:      db.OperationInstanceCreate,
		UUID:      "abcd",
		ProjectID: &projectID,
	}
	id, err := tx.CreateOrReplaceOperation(opInfo)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	operations, err := tx.GetOperationsByNodeID(*tx.GetNodeID(), db.OperationFilter{})
	require.NoError(t, err)
	assert.Len(t, operations, 1)
	assert.Equal(t, operations[0].UUID, "abcd")

	ops, err := tx.GetOperationsByUUID("abcd", db.OperationFilter{})
	require.NoError(t, err)
	assert.Equal(t, len(ops), 1)
	operation := ops[0]
	assert.Equal(t, id, operation.ID)
	assert.Equal(t, db.OperationInstanceCreate, operation.Type)

	ops, err = tx.GetOperationsByNodeID(*tx.GetNodeID(), db.OperationFilter{})
	require.NoError(t, err)
	assert.Equal(t, "abcd", ops[0].UUID)

	err = tx.DeleteOperationByUUID("abcd", db.OperationFilter{})
	require.NoError(t, err)

	ops, err = tx.GetOperationsByUUID("abcd", db.OperationFilter{})
	require.NoError(t, err)
	assert.Equal(t, len(ops), 0)
}

// Add, get and remove an operation not associated with any project.
func TestOperationNoProject(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	opInfo := db.Operation{
		NodeID: *tx.GetNodeID(),
		Type:   db.OperationInstanceCreate,
		UUID:   "abcd",
	}

	id, err := tx.CreateOrReplaceOperation(opInfo)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	operations, err := tx.GetOperationsByNodeID(*tx.GetNodeID(), db.OperationFilter{})
	require.NoError(t, err)
	assert.Len(t, operations, 1)
	assert.Equal(t, operations[0].UUID, "abcd")

	ops, err := tx.GetOperationsByUUID("abcd", db.OperationFilter{})
	require.NoError(t, err)
	assert.Equal(t, len(ops), 1)
	operation := ops[0]
	require.NoError(t, err)
	assert.Equal(t, id, operation.ID)
	assert.Equal(t, db.OperationInstanceCreate, operation.Type)

	ops, err = tx.GetOperationsByNodeID(*tx.GetNodeID(), db.OperationFilter{})
	require.NoError(t, err)
	assert.Equal(t, "abcd", ops[0].UUID)

	err = tx.DeleteOperationByUUID("abcd", db.OperationFilter{})
	require.NoError(t, err)

	ops, err = tx.GetOperationsByUUID("abcd", db.OperationFilter{})
	require.NoError(t, err)
	assert.Equal(t, len(ops), 0)
}
