package db_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoragePoolsCreatePending(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.NodeAdd("buzz", "1.2.3.4:666")
	require.NoError(t, err)
	_, err = tx.NodeAdd("rusp", "5.6.7.8:666")
	require.NoError(t, err)

	config := map[string]string{"source": "/foo"}
	err = tx.StoragePoolCreatePending("buzz", "pool1", "dir", config)
	require.NoError(t, err)

	poolID, err := tx.StoragePoolID("pool1")
	require.NoError(t, err)
	assert.True(t, poolID > 0)

	config = map[string]string{"source": "/bar"}
	err = tx.StoragePoolCreatePending("rusp", "pool1", "dir", config)
	require.NoError(t, err)

	// The initial node (whose name is 'none' by default) is missing.
	_, err = tx.StoragePoolNodeConfigs(poolID)
	require.EqualError(t, err, "Pool not defined on nodes: none")

	config = map[string]string{"source": "/egg"}
	err = tx.StoragePoolCreatePending("none", "pool1", "dir", config)
	require.NoError(t, err)

	// Now the storage is defined on all nodes.
	configs, err := tx.StoragePoolNodeConfigs(poolID)
	require.NoError(t, err)
	assert.Len(t, configs, 3)
	assert.Equal(t, map[string]string{"source": "/foo"}, configs["buzz"])
	assert.Equal(t, map[string]string{"source": "/bar"}, configs["rusp"])
	assert.Equal(t, map[string]string{"source": "/egg"}, configs["none"])
}

// If an entry for the given pool and node already exists, an error is
// returned.
func TestStoragePoolsCreatePending_AlreadyDefined(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.NodeAdd("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	err = tx.StoragePoolCreatePending("buzz", "pool1", "dir", map[string]string{})
	require.NoError(t, err)

	err = tx.StoragePoolCreatePending("buzz", "pool1", "dir", map[string]string{})
	require.Equal(t, db.DbErrAlreadyDefined, err)
}

// If no node with the given name is found, an error is returned.
func TestStoragePoolsCreatePending_NonExistingNode(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	err := tx.StoragePoolCreatePending("buzz", "pool1", "dir", map[string]string{})
	require.Equal(t, db.NoSuchObjectError, err)
}

// If a pool with the given name already exists but has different driver, an
// error is returned.
func TestStoragePoolsCreatePending_DriverMismatch(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.NodeAdd("buzz", "1.2.3.4:666")
	require.NoError(t, err)
	_, err = tx.NodeAdd("rusp", "5.6.7.8:666")
	require.NoError(t, err)

	err = tx.StoragePoolCreatePending("buzz", "pool1", "dir", map[string]string{})
	require.NoError(t, err)

	err = tx.StoragePoolCreatePending("rusp", "pool1", "zfs", map[string]string{})
	require.EqualError(t, err, "pool already exists with a different driver")
}
