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

	id, err := tx.OperationAdd("default", "abcd", db.OperationContainerCreate)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	operations, err := tx.Operations()
	require.NoError(t, err)
	assert.Len(t, operations, 1)
	assert.Equal(t, operations[0].UUID, "abcd")

	operation, err := tx.OperationByUUID("abcd")
	require.NoError(t, err)
	assert.Equal(t, id, operation.ID)
	assert.Equal(t, db.OperationContainerCreate, operation.Type)

	uuids, err := tx.OperationsUUIDs()
	require.NoError(t, err)
	assert.Equal(t, []string{"abcd"}, uuids)

	err = tx.OperationRemove("abcd")
	require.NoError(t, err)

	_, err = tx.OperationByUUID("abcd")
	assert.Equal(t, db.ErrNoSuchObject, err)
}

// Add, get and remove an operation not associated with any project.
func TestOperationNoProject(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.OperationAdd("", "abcd", db.OperationContainerCreate)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	operations, err := tx.Operations()
	require.NoError(t, err)
	assert.Len(t, operations, 1)
	assert.Equal(t, operations[0].UUID, "abcd")

	operation, err := tx.OperationByUUID("abcd")
	require.NoError(t, err)
	assert.Equal(t, id, operation.ID)
	assert.Equal(t, db.OperationContainerCreate, operation.Type)

	uuids, err := tx.OperationsUUIDs()
	require.NoError(t, err)
	assert.Equal(t, []string{"abcd"}, uuids)

	err = tx.OperationRemove("abcd")
	require.NoError(t, err)

	_, err = tx.OperationByUUID("abcd")
	assert.Equal(t, db.ErrNoSuchObject, err)
}
