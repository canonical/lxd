package db_test

import (
	"testing"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Containers are grouped by node address.
func TestContainersListByNodeAddress(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	nodeID2, err := tx.NodeAdd("node2", "1.2.3.4:666")
	require.NoError(t, err)

	nodeID3, err := tx.NodeAdd("node3", "5.6.7.8:666")
	require.NoError(t, err)
	require.NoError(t, tx.NodeHeartbeat("5.6.7.8:666", time.Now().Add(-time.Minute)))

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")
	addContainer(t, tx, nodeID3, "c3")
	addContainer(t, tx, nodeID2, "c4")

	result, err := tx.ContainersListByNodeAddress()
	require.NoError(t, err)
	assert.Equal(
		t,
		map[string][]string{
			"":            {"c2"},
			"1.2.3.4:666": {"c1", "c4"},
			"0.0.0.0":     {"c3"},
		}, result)
}

// Containers are associated with their node name.
func TestContainersByNodeName(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	nodeID2, err := tx.NodeAdd("node2", "1.2.3.4:666")
	require.NoError(t, err)

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")

	result, err := tx.ContainersByNodeName()
	require.NoError(t, err)
	assert.Equal(
		t,
		map[string]string{
			"c1": "node2",
			"c2": "none",
		}, result)
}

func TestContainerPool(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	poolID, err := cluster.StoragePoolCreate("default", "", "dir", nil)
	require.NoError(t, err)
	_, err = cluster.StoragePoolVolumeCreate("c1", "", db.StoragePoolVolumeTypeContainer, poolID, nil)
	require.NoError(t, err)

	args := db.ContainerArgs{
		Name: "c1",
		Devices: types.Devices{
			"root": types.Device{
				"path": "/",
				"pool": "default",
				"type": "disk",
			},
		},
	}
	_, err = cluster.ContainerCreate(args)
	require.NoError(t, err)
	poolName, err := cluster.ContainerPool("c1")
	require.NoError(t, err)
	assert.Equal(t, "default", poolName)
}

// Only containers running on the local node are returned.
func TestContainersNodeList(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	// Add another node
	var nodeID2 int64
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		nodeID2, err = tx.NodeAdd("node2", "1.2.3.4:666")
		require.NoError(t, err)
		addContainer(t, tx, nodeID1, "c1")
		addContainer(t, tx, nodeID2, "c2")
		return nil
	})
	require.NoError(t, err)

	names, err := cluster.ContainersNodeList(db.CTypeRegular)
	require.NoError(t, err)
	assert.Equal(t, names, []string{"c1"})
}

func addContainer(t *testing.T, tx *db.ClusterTx, nodeID int64, name string) {
	stmt := `
INSERT INTO containers(node_id, name, architecture, type) VALUES (?, ?, 1, ?)
`
	_, err := tx.Tx().Exec(stmt, nodeID, name, db.CTypeRegular)
	require.NoError(t, err)
}
