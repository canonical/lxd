//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/shared/api"
)

// Addresses of all nodes with matching volume name are returned.
func TestGetStorageVolumeNodes(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local member

	nodeID2, err := tx.CreateNode("node2", "1.2.3.4:666")
	require.NoError(t, err)

	nodeID3, err := tx.CreateNode("node3", "5.6.7.8:666")
	require.NoError(t, err)

	poolID := addPool(t, tx, "pool1")
	addVolume(t, tx, poolID, nodeID1, "volume1")
	addVolume(t, tx, poolID, nodeID2, "volume1")
	addVolume(t, tx, poolID, nodeID3, "volume2")
	addVolume(t, tx, poolID, nodeID2, "volume2")

	nodes, err := tx.GetStorageVolumeNodes(context.Background(), poolID, "default", "volume1", 1)
	require.NoError(t, err)

	assert.Equal(t, []db.NodeInfo{
		{
			ID:      nodeID1,
			Name:    "none",
			Address: "0.0.0.0",
		},
		{
			ID:      nodeID2,
			Name:    "node2",
			Address: "1.2.3.4:666",
		},
	}, nodes)
}

func TestGetRenameAndDeleteStoragePoolVolumeBackup(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	poolID1 := addPool(t, tx, "pool1")
	poolID2 := addPool(t, tx, "pool2")
	volumeID1 := addVolume(t, tx, poolID1, 1, "volume1")
	volumeID2 := addVolume(t, tx, poolID2, 1, "volume1")
	_, err := tx.Tx().Exec("UPDATE storage_volumes SET type=? WHERE id IN (?, ?)", cluster.StoragePoolVolumeTypeCustom, volumeID1, volumeID2)
	require.NoError(t, err)

	creationDate := time.Now().Unix()
	_, err = tx.Tx().Exec("INSERT INTO storage_volumes_backups (storage_volume_id, name, creation_date, expiry_date) VALUES (?, ?, ?, ?), (?, ?, ?, ?)", volumeID1, "volume1/backup", creationDate, creationDate, volumeID2, "volume1/backup", creationDate, creationDate)
	require.NoError(t, err)

	backup1, err := tx.GetStoragePoolVolumeBackup(context.Background(), "default", "pool1", "volume1/backup")
	require.NoError(t, err)
	backup2, err := tx.GetStoragePoolVolumeBackup(context.Background(), "default", "pool2", "volume1/backup")
	require.NoError(t, err)
	assert.NotEqual(t, backup1.ID, backup2.ID)

	expired, err := tx.GetExpiredStorageVolumeBackups(context.Background())
	require.NoError(t, err)
	expiredIDs := make([]int, 0, len(expired))
	for _, b := range expired {
		expiredIDs = append(expiredIDs, b.ID)
	}

	assert.ElementsMatch(t, []int{backup1.ID, backup2.ID}, expiredIDs)

	err = tx.RenameVolumeBackup(context.Background(), -1, "volume1/renamed")
	assert.True(t, api.StatusErrorCheck(err, http.StatusNotFound))

	err = tx.RenameVolumeBackup(context.Background(), backup1.ID, "volume1/renamed")
	require.NoError(t, err)

	_, err = tx.GetStoragePoolVolumeBackup(context.Background(), "default", "pool1", "volume1/renamed")
	require.NoError(t, err)
	_, err = tx.GetStoragePoolVolumeBackup(context.Background(), "default", "pool2", "volume1/backup")
	require.NoError(t, err)

	err = tx.DeleteStoragePoolVolumeBackup(context.Background(), -1)
	assert.True(t, api.StatusErrorCheck(err, http.StatusNotFound))

	err = tx.DeleteStoragePoolVolumeBackup(context.Background(), backup2.ID)
	require.NoError(t, err)

	_, err = tx.GetStoragePoolVolumeBackup(context.Background(), "default", "pool2", "volume1/backup")
	assert.True(t, api.StatusErrorCheck(err, http.StatusNotFound))
	_, err = tx.GetStoragePoolVolumeBackup(context.Background(), "default", "pool1", "volume1/renamed")
	require.NoError(t, err)
}

func addPool(t *testing.T, tx *db.ClusterTx, name string) int64 {
	stmt := `
INSERT INTO storage_pools(name, driver, description) VALUES (?, 'dir', '')
`
	result, err := tx.Tx().Exec(stmt, name)
	require.NoError(t, err)

	id, err := result.LastInsertId()
	require.NoError(t, err)

	return id
}

func addVolume(t *testing.T, tx *db.ClusterTx, poolID, nodeID int64, name string) int64 {
	stmt := `
INSERT INTO storage_volumes(storage_pool_id, node_id, name, type, project_id, description) VALUES (?, ?, ?, 1, 1, '')
`
	result, err := tx.Tx().Exec(stmt, poolID, nodeID, name)
	require.NoError(t, err)

	id, err := result.LastInsertId()
	require.NoError(t, err)

	return id
}
