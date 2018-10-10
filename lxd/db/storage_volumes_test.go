package db_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Addresses of all nodes with matching volume name are returned.
func TestStorageVolumeNodeAddresses(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	nodeID2, err := tx.NodeAdd("node2", "1.2.3.4:666")
	require.NoError(t, err)

	nodeID3, err := tx.NodeAdd("node3", "5.6.7.8:666")
	require.NoError(t, err)

	poolID := addPool(t, tx, "pool1")
	addVolume(t, tx, poolID, nodeID1, "volume1")
	addVolume(t, tx, poolID, nodeID2, "volume1")
	addVolume(t, tx, poolID, nodeID3, "volume2")
	addVolume(t, tx, poolID, nodeID2, "volume2")

	addresses, err := tx.StorageVolumeNodeAddresses(poolID, "default", "volume1", 1)
	require.NoError(t, err)

	assert.Equal(t, []string{"", "1.2.3.4:666"}, addresses)
}

func addPool(t *testing.T, tx *db.ClusterTx, name string) int64 {
	stmt := `
INSERT INTO storage_pools(name, driver) VALUES (?, 'dir')
`
	result, err := tx.Tx().Exec(stmt, name)
	require.NoError(t, err)

	id, err := result.LastInsertId()
	require.NoError(t, err)

	return id
}

func addVolume(t *testing.T, tx *db.ClusterTx, poolID, nodeID int64, name string) {
	stmt := `
INSERT INTO storage_volumes(storage_pool_id, node_id, name, type, project_id) VALUES (?, ?, ?, 1, 1)
`
	_, err := tx.Tx().Exec(stmt, poolID, nodeID, name)
	require.NoError(t, err)
}
