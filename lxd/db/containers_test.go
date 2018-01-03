package db_test

import (
	"testing"
	"time"

	"github.com/lxc/lxd/lxd/db"
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

func addContainer(t *testing.T, tx *db.ClusterTx, nodeID int64, name string) {
	stmt := `
INSERT INTO containers(node_id, name, architecture, type) VALUES (?, ?, 1, ?)
`
	_, err := tx.Tx().Exec(stmt, nodeID, name, db.CTypeRegular)
	require.NoError(t, err)
}
