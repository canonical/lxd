//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
)

// Add, get and remove an operation.
func TestOperation(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	projectID, err := cluster.GetProjectID(context.Background(), tx.Tx(), "default")
	require.NoError(t, err)
	nodeID := tx.GetNodeID()
	uuid := "abcd"

	opInfo := cluster.OperationsRow{
		NodeID:    nodeID,
		Type:      operationtype.InstanceCreate,
		UUID:      uuid,
		ProjectID: &projectID,
	}

	id, err := query.CreateOrReplace(context.TODO(), tx.Tx(), opInfo)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	operations, err := cluster.GetOperationsByNodeID(context.TODO(), tx.Tx(), nodeID)
	require.NoError(t, err)
	assert.Len(t, operations, 1)
	assert.Equal(t, "abcd", operations[0].Row.UUID)

	operation, err := cluster.GetOperation(context.TODO(), tx.Tx(), uuid)
	require.NoError(t, err)
	assert.Equal(t, id, operation.Row.ID)
	assert.Equal(t, operationtype.InstanceCreate, operation.Row.Type)

	ops, err := cluster.GetOperationsByNodeID(context.TODO(), tx.Tx(), nodeID)
	require.NoError(t, err)
	assert.Equal(t, "abcd", ops[0].Row.UUID)

	err = cluster.DeleteOperations(context.TODO(), tx.Tx(), "abcd")
	require.NoError(t, err)

	operation, err = cluster.GetOperation(context.TODO(), tx.Tx(), uuid)
	require.Nil(t, operation)
	var target api.StatusError
	require.ErrorAs(t, err, &target)
	require.Equal(t, http.StatusNotFound, target.Status())
}

func TestOperationBulkDelete(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	testOps := make([]cluster.OperationsRow, 2002)
	testOpIDs := make([]string, 0, 2002)
	for i := range testOps {
		id := uuid.NewString()
		testOpIDs = append(testOpIDs, id)
		testOps[i] = cluster.OperationsRow{
			NodeID:     tx.GetNodeID(),
			UUID:       id,
			Metadata:   "{}",
			Class:      1,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
			Inputs:     "{}",
			StatusCode: int64(api.Running),
		}
	}

	for _, op := range testOps {
		_, err := query.Create(t.Context(), tx.Tx(), op)
		require.NoError(t, err)
	}

	err := cluster.DeleteOperations(t.Context(), tx.Tx(), testOpIDs...)
	require.NoError(t, err)
}

// Add, get and remove an operation not associated with any project.
func TestOperationNoProject(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID := tx.GetNodeID()
	uuid := "abcd"

	opInfo := cluster.OperationsRow{
		NodeID: nodeID,
		Type:   operationtype.InstanceCreate,
		UUID:   uuid,
	}

	id, err := query.CreateOrReplace(context.TODO(), tx.Tx(), opInfo)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	operations, err := cluster.GetOperationsByNodeID(context.TODO(), tx.Tx(), nodeID)
	require.NoError(t, err)
	assert.Len(t, operations, 1)
	assert.Equal(t, "abcd", operations[0].Row.UUID)

	operation, err := cluster.GetOperation(context.TODO(), tx.Tx(), uuid)
	require.NoError(t, err)
	assert.Equal(t, id, operation.Row.ID)
	assert.Equal(t, operationtype.InstanceCreate, operation.Row.Type)

	ops, err := cluster.GetOperationsByNodeID(context.TODO(), tx.Tx(), nodeID)
	require.NoError(t, err)
	assert.Equal(t, "abcd", ops[0].Row.UUID)

	err = cluster.DeleteOperations(context.TODO(), tx.Tx(), "abcd")
	require.NoError(t, err)

	operation, err = cluster.GetOperation(context.TODO(), tx.Tx(), uuid)
	require.Nil(t, operation)
	var target api.StatusError
	require.ErrorAs(t, err, &target)
	require.Equal(t, http.StatusNotFound, target.Status())
}

// TestOperationUpdate tests that [cluster.UpdateOperation] requires that the operation is running on the given node.
func TestOperationUpdate(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID := tx.GetNodeID()
	uuid := "abcd"

	opInfo := cluster.OperationsRow{
		NodeID: nodeID,
		Type:   operationtype.InstanceCreate,
		UUID:   uuid,
	}

	id, err := query.Create(t.Context(), tx.Tx(), opInfo)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	updatedAt := time.Now().UTC()
	err = cluster.UpdateOperation(t.Context(), tx.Tx(), uuid, nodeID, updatedAt, api.Running, map[string]any{"foo": "bar"}, "", 0)
	require.NoError(t, err)

	operation, err := cluster.GetOperation(context.TODO(), tx.Tx(), uuid)
	require.NoError(t, err)
	assert.Equal(t, id, operation.Row.ID)
	assert.Equal(t, operationtype.InstanceCreate, operation.Row.Type)
	assert.Equal(t, int64(api.Running), operation.Row.StatusCode)
	assert.JSONEq(t, `{"foo":"bar"}`, operation.Row.Metadata)
	assert.Equal(t, updatedAt, operation.Row.UpdatedAt)

	incorrectNodeID := nodeID + 1
	err = cluster.UpdateOperation(t.Context(), tx.Tx(), uuid, incorrectNodeID, updatedAt, api.Running, map[string]any{"foo": "bar"}, "", 0)
	var statusErr api.StatusError
	assert.ErrorAs(t, err, &statusErr)
	assert.Equal(t, http.StatusConflict, statusErr.Status())

	err = cluster.DeleteOperations(context.TODO(), tx.Tx(), "abcd")
	require.NoError(t, err)
}
