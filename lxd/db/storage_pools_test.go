package db_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The StoragePoolsNodeConfigs method returns only node-specific config values.
func TestStoragePoolsNodeConfigs(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	// Create a storage pool named "local" (like the default LXD clustering
	// one), then delete it and create another one.
	_, err := cluster.StoragePoolCreate("local", "", "dir", map[string]string{
		"rsync.bwlimit": "1",
		"source":        "/foo/bar",
	})
	require.NoError(t, err)

	_, err = cluster.StoragePoolDelete("local")
	require.NoError(t, err)

	_, err = cluster.StoragePoolCreate("BTRFS", "", "dir", map[string]string{
		"rsync.bwlimit": "1",
		"source":        "/egg/baz",
	})
	require.NoError(t, err)

	// Check that the config map returned by StoragePoolsConfigs actually
	// contains the value of the "BTRFS" storage pool.
	var config map[string]map[string]string

	err = cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		config, err = tx.StoragePoolsNodeConfig()
		return err
	})
	require.NoError(t, err)

	assert.Equal(t, config, map[string]map[string]string{
		"BTRFS": {"source": "/egg/baz"},
	})
}

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

func TestStoragePoolsCreatePending_OtherPool(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	// Create a pending pool named 'pool1' on two nodes (the default 'none'
	// and 'buzz')
	_, err := tx.NodeAdd("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	config := map[string]string{"source": "/foo"}
	err = tx.StoragePoolCreatePending("none", "pool1", "dir", config)
	require.NoError(t, err)

	config = map[string]string{"source": "/bar"}
	err = tx.StoragePoolCreatePending("buzz", "pool1", "dir", config)
	require.NoError(t, err)

	// Create a second pending pool named pool2 on the same two nodes.
	config = map[string]string{}
	err = tx.StoragePoolCreatePending("none", "pool2", "dir", config)
	require.NoError(t, err)

	poolID, err := tx.StoragePoolID("pool2")
	require.NoError(t, err)

	config = map[string]string{}
	err = tx.StoragePoolCreatePending("buzz", "pool2", "dir", config)
	require.NoError(t, err)

	// The node-level configs of the second pool do not contain any key
	// from the first pool.
	configs, err := tx.StoragePoolNodeConfigs(poolID)
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

	_, err := tx.NodeAdd("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	err = tx.StoragePoolCreatePending("buzz", "pool1", "dir", map[string]string{})
	require.NoError(t, err)

	err = tx.StoragePoolCreatePending("buzz", "pool1", "dir", map[string]string{})
	require.Equal(t, db.ErrAlreadyDefined, err)
}

// If no node with the given name is found, an error is returned.
func TestStoragePoolsCreatePending_NonExistingNode(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	err := tx.StoragePoolCreatePending("buzz", "pool1", "dir", map[string]string{})
	require.Equal(t, db.ErrNoSuchObject, err)
}

// If a pool with the given name already exists but has different driver, an
// error is returned. Likewise, if volume is updated or deleted, it's updated
// or deleted on all nodes.
func TestStoragePoolVolume_Ceph(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	// Create a second node (beyond the default one).
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		_, err := tx.NodeAdd("n1", "1.2.3.4:666")
		return err
	})
	require.NoError(t, err)

	poolID, err := cluster.StoragePoolCreate("p1", "", "ceph", nil)
	require.NoError(t, err)

	config := map[string]string{"k": "v"}
	volumeID, err := cluster.StoragePoolVolumeCreate("default", "v1", "", 1, false, poolID, config)
	require.NoError(t, err)

	// The returned volume ID is the one of the volume created on the local
	// node (node 1).
	thisVolumeID, _, err := cluster.StoragePoolVolumeGetType("default", "v1", 1, poolID, 1)
	require.NoError(t, err)
	assert.Equal(t, volumeID, thisVolumeID)

	// Another volume was created for the second node.
	_, volume, err := cluster.StoragePoolVolumeGetType("default", "v1", 1, poolID, 2)
	require.NoError(t, err)
	assert.NotNil(t, volume)
	assert.Equal(t, config, volume.Config)

	// Update the volume
	config["k"] = "v2"
	err = cluster.StoragePoolVolumeUpdate("v1", 1, poolID, "volume 1", config)
	require.NoError(t, err)
	for _, nodeID := range []int64{1, 2} {
		_, volume, err := cluster.StoragePoolVolumeGetType("default", "v1", 1, poolID, nodeID)
		require.NoError(t, err)
		assert.Equal(t, "volume 1", volume.Description)
		assert.Equal(t, config, volume.Config)
	}
	err = cluster.StoragePoolVolumeRename("default", "v1", "v1-new", 1, poolID)
	require.NoError(t, err)
	for _, nodeID := range []int64{1, 2} {
		_, volume, err := cluster.StoragePoolVolumeGetType("default", "v1-new", 1, poolID, nodeID)
		require.NoError(t, err)
		assert.NotNil(t, volume)
	}
	require.NoError(t, err)

	// Delete the volume
	err = cluster.StoragePoolVolumeDelete("default", "v1-new", 1, poolID)
	require.NoError(t, err)
	for _, nodeID := range []int64{1, 2} {
		_, volume, err := cluster.StoragePoolVolumeGetType("default", "v1-new", 1, poolID, nodeID)
		assert.Equal(t, db.ErrNoSuchObject, err)
		assert.Nil(t, volume)
	}
}
