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

	id, err := tx.OperationAdd("abcd")
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	operation, err := tx.OperationByUUID("abcd")
	require.NoError(t, err)
	assert.Equal(t, id, operation.ID)

	uuids, err := tx.OperationsUUIDs()
	require.NoError(t, err)
	assert.Equal(t, []string{"abcd"}, uuids)

	err = tx.OperationRemove("abcd")
	require.NoError(t, err)

	_, err = tx.OperationByUUID("abcd")
	assert.Equal(t, db.ErrNoSuchObject, err)
}
