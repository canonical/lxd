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
// error is returned. Likewise, if volume is updated or deleted, it's updated
// or deleted on all nodes.
func TestStoragePoolVolume_Ceph(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	// Create a second no (beyond the default one).
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		_, err := tx.NodeAdd("n1", "1.2.3.4:666")
		return err
	})
	require.NoError(t, err)

	poolID, err := cluster.StoragePoolCreate("p1", "", "ceph", nil)
	require.NoError(t, err)

	config := map[string]string{"k": "v"}
	volumeID, err := cluster.StoragePoolVolumeCreate("v1", "", 1, poolID, config)
	require.NoError(t, err)

	// The returned volume ID is the one of the volume created on the local
	// node (node 1).
	thisVolumeID, _, err := cluster.StoragePoolVolumeGetType("v1", 1, poolID, 1)
	require.NoError(t, err)
	assert.Equal(t, volumeID, thisVolumeID)

	// Another volume was created for the second node.
	_, volume, err := cluster.StoragePoolVolumeGetType("v1", 1, poolID, 2)
	require.NoError(t, err)
	assert.NotNil(t, volume)
	assert.Equal(t, config, volume.Config)

	// Update the volume
	config["k"] = "v2"
	err = cluster.StoragePoolVolumeUpdate("v1", 1, poolID, "volume 1", config)
	require.NoError(t, err)
	for _, nodeID := range []int64{1, 2} {
		_, volume, err := cluster.StoragePoolVolumeGetType("v1", 1, poolID, nodeID)
		require.NoError(t, err)
		assert.Equal(t, "volume 1", volume.Description)
		assert.Equal(t, config, volume.Config)
	}
	err = cluster.StoragePoolVolumeRename("v1", "v1-new", 1, poolID)
	require.NoError(t, err)
	for _, nodeID := range []int64{1, 2} {
		_, volume, err := cluster.StoragePoolVolumeGetType("v1-new", 1, poolID, nodeID)
		require.NoError(t, err)
		assert.NotNil(t, volume)
	}
	require.NoError(t, err)

	// Delete the volume
	err = cluster.StoragePoolVolumeDelete("v1-new", 1, poolID)
	require.NoError(t, err)
	for _, nodeID := range []int64{1, 2} {
		_, volume, err := cluster.StoragePoolVolumeGetType("v1-new", 1, poolID, nodeID)
		assert.Equal(t, db.NoSuchObjectError, err)
		assert.Nil(t, volume)
	}
}
