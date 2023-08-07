//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/response"
)

// Initializes the package by setting the supported remote storage driver names.
func init() {
	db.StorageRemoteDriverNames = func() []string {
		return []string{"ceph", "cephfs"}
	}
}

// The GetStoragePoolsLocalConfigs method returns only node-specific config values.
func TestGetStoragePoolsLocalConfigs(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	// Create a storage pool named "local" (like the default LXD clustering
	// one), then delete it and create another one.
	_, err := cluster.CreateStoragePool("local", "", "dir", map[string]string{
		"rsync.bwlimit": "1",
		"source":        "/foo/bar",
	})
	require.NoError(t, err)

	_, err = cluster.RemoveStoragePool("local")
	require.NoError(t, err)

	_, err = cluster.CreateStoragePool("BTRFS", "", "dir", map[string]string{
		"rsync.bwlimit": "1",
		"source":        "/egg/baz",
	})
	require.NoError(t, err)

	// Check that the config map returned by StoragePoolsConfigs actually
	// contains the value of the "BTRFS" storage pool.
	var config map[string]map[string]string

	err = cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		config, err = tx.GetStoragePoolsLocalConfig(ctx)
		return err
	})
	require.NoError(t, err)

	assert.Equal(t, config, map[string]map[string]string{
		"BTRFS": {"source": "/egg/baz"},
	})
}

// Tests the creation of pending storage pools across multiple nodes, validating their configuration.
func TestStoragePoolsCreatePending(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)
	_, err = tx.CreateNode("rusp", "5.6.7.8:666")
	require.NoError(t, err)

	config := map[string]string{"source": "/foo"}
	err = tx.CreatePendingStoragePool(context.Background(), "buzz", "pool1", "dir", config)
	require.NoError(t, err)

	poolID, err := tx.GetStoragePoolID(context.Background(), "pool1")
	require.NoError(t, err)
	assert.True(t, poolID > 0)

	config = map[string]string{"source": "/bar"}
	err = tx.CreatePendingStoragePool(context.Background(), "rusp", "pool1", "dir", config)
	require.NoError(t, err)

	// The initial node (whose name is 'none' by default) is missing.
	_, err = tx.GetStoragePoolNodeConfigs(context.Background(), poolID)
	require.EqualError(t, err, "Pool not defined on nodes: none")

	config = map[string]string{"source": "/egg"}
	err = tx.CreatePendingStoragePool(context.Background(), "none", "pool1", "dir", config)
	require.NoError(t, err)

	// Now the storage is defined on all nodes.
	configs, err := tx.GetStoragePoolNodeConfigs(context.Background(), poolID)
	require.NoError(t, err)
	assert.Len(t, configs, 3)
	assert.Equal(t, map[string]string{"source": "/foo"}, configs["buzz"])
	assert.Equal(t, map[string]string{"source": "/bar"}, configs["rusp"])
	assert.Equal(t, map[string]string{"source": "/egg"}, configs["none"])
}

// Tests the creation of multiple pending storage pools across nodes, ensuring they maintain distinct configurations.
func TestStoragePoolsCreatePending_OtherPool(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	// Create a pending pool named 'pool1' on two nodes (the default 'none'
	// and 'buzz')
	_, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	config := map[string]string{"source": "/foo"}
	err = tx.CreatePendingStoragePool(context.Background(), "none", "pool1", "dir", config)
	require.NoError(t, err)

	config = map[string]string{"source": "/bar"}
	err = tx.CreatePendingStoragePool(context.Background(), "buzz", "pool1", "dir", config)
	require.NoError(t, err)

	// Create a second pending pool named pool2 on the same two nodes.
	config = map[string]string{}
	err = tx.CreatePendingStoragePool(context.Background(), "none", "pool2", "dir", config)
	require.NoError(t, err)

	poolID, err := tx.GetStoragePoolID(context.Background(), "pool2")
	require.NoError(t, err)

	config = map[string]string{}
	err = tx.CreatePendingStoragePool(context.Background(), "buzz", "pool2", "dir", config)
	require.NoError(t, err)

	// The node-level configs of the second pool do not contain any key
	// from the first pool.
	configs, err := tx.GetStoragePoolNodeConfigs(context.Background(), poolID)
	require.NoError(t, err)
	assert.Len(t, configs, 2)
	assert.Equal(t, map[string]string{}, configs["none"])
	assert.Equal(t, map[string]string{}, configs["buzz"])
}

// If an entry for the given pool and node already exists, an error is
// returned.
func TestStoragePoolsCreatePending_AlreadyDefined(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	err = tx.CreatePendingStoragePool(context.Background(), "buzz", "pool1", "dir", map[string]string{})
	require.NoError(t, err)

	err = tx.CreatePendingStoragePool(context.Background(), "buzz", "pool1", "dir", map[string]string{})
	require.Equal(t, db.ErrAlreadyDefined, err)
}

// If no node with the given name is found, an error is returned.
func TestStoragePoolsCreatePending_NonExistingNode(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	err := tx.CreatePendingStoragePool(context.Background(), "buzz", "pool1", "dir", map[string]string{})
	require.True(t, response.IsNotFoundError(err))
}

// If a pool with the given name already exists but has different driver, an
// error is returned. Likewise, if volume is updated or deleted, it's updated
// or deleted on all nodes.
func TestStoragePoolVolume_Ceph(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	// Create a second node (beyond the default one).
	err := cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateNode("n1", "1.2.3.4:666")
		return err
	})
	require.NoError(t, err)

	poolID, err := cluster.CreateStoragePool("p1", "", "ceph", nil)
	require.NoError(t, err)

	config := map[string]string{"k": "v"}
	volumeID, err := cluster.CreateStoragePoolVolume("default", "v1", "", 1, poolID, config, db.StoragePoolVolumeContentTypeFS, time.Now())
	require.NoError(t, err)

	getStoragePoolVolume := func(volumeProjectName string, volumeName string, volumeType int, poolID int64) (*db.StorageVolume, error) {
		var dbVolume *db.StorageVolume
		err = cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			dbVolume, err = tx.GetStoragePoolVolume(context.Background(), poolID, volumeProjectName, volumeType, volumeName, true)
			return err
		})
		if err != nil {
			return nil, err
		}

		return dbVolume, nil
	}

	// The returned volume ID is the one of the volume created on the local
	// node (node 1).
	thisVolume, err := getStoragePoolVolume("default", "v1", 1, poolID)
	require.NoError(t, err)
	assert.NotNil(t, thisVolume)
	assert.Equal(t, volumeID, thisVolume.ID)
	assert.Equal(t, thisVolume.Location, "")

	// Update the volume
	config["k"] = "v2"
	err = cluster.UpdateStoragePoolVolume("default", "v1", 1, poolID, "volume 1", config)
	require.NoError(t, err)
	volume, err := getStoragePoolVolume("default", "v1", 1, poolID)
	require.NoError(t, err)
	assert.Equal(t, "volume 1", volume.Description)
	assert.Equal(t, config, volume.Config)

	err = cluster.RenameStoragePoolVolume("default", "v1", "v1-new", 1, poolID)
	require.NoError(t, err)
	volume, err = getStoragePoolVolume("default", "v1-new", 1, poolID)
	require.NoError(t, err)
	assert.NotNil(t, volume)

	// Delete the volume
	err = cluster.RemoveStoragePoolVolume("default", "v1-new", 1, poolID)
	require.NoError(t, err)
	volume, err = getStoragePoolVolume("default", "v1-new", 1, poolID)
	assert.True(t, response.IsNotFoundError(err))
	assert.Nil(t, volume)
}

// Test creating a volume snapshot.
func TestCreateStoragePoolVolume_Snapshot(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	poolID, err := cluster.CreateStoragePool("p1", "", "dir", nil)
	require.NoError(t, err)

	poolID1, err := cluster.CreateStoragePool("p2", "", "dir", nil)
	require.NoError(t, err)

	config := map[string]string{"k": "v"}
	_, err = cluster.CreateStoragePoolVolume("default", "v1", "", 1, poolID, config, db.StoragePoolVolumeContentTypeFS, time.Now())
	require.NoError(t, err)

	_, err = cluster.CreateStoragePoolVolume("default", "v1", "", 1, poolID1, config, db.StoragePoolVolumeContentTypeFS, time.Now())
	require.NoError(t, err)

	config = map[string]string{"k": "v"}
	_, err = cluster.CreateStorageVolumeSnapshot("default", "v1/snap0", "", 1, poolID, config, time.Now(), time.Time{})
	require.NoError(t, err)

	n := cluster.GetNextStorageVolumeSnapshotIndex("p1", "v1", 1, "snap%d")
	assert.Equal(t, n, 1)

	n = cluster.GetNextStorageVolumeSnapshotIndex("p2", "v1", 1, "snap%d")
	assert.Equal(t, n, 0)
}
