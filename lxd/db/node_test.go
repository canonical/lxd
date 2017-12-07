package db_test

import (
	"testing"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/shared/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Add a new raft node.
func TestNodeAdd(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.NodeAdd("buzz", "1.2.3.4:666")
	require.NoError(t, err)
	assert.Equal(t, int64(2), id)

	nodes, err := tx.Nodes()
	require.NoError(t, err)
	require.Len(t, nodes, 2)

	node, err := tx.NodeByAddress("1.2.3.4:666")
	require.NoError(t, err)
	assert.Equal(t, "buzz", node.Name)
	assert.Equal(t, "1.2.3.4:666", node.Address)
	assert.Equal(t, cluster.SchemaVersion, node.Schema)
	assert.Equal(t, len(version.APIExtensions), node.APIExtensions)
	assert.False(t, node.IsDown())

	node, err = tx.NodeByName("buzz")
	require.NoError(t, err)
	assert.Equal(t, "buzz", node.Name)
}

func TestNodesCount(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	count, err := tx.NodesCount()
	require.NoError(t, err)
	assert.Equal(t, 1, count) // There's always at least one node.

	_, err = tx.NodeAdd("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	count, err = tx.NodesCount()
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestNodeName(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	name, err := tx.NodeName()
	require.NoError(t, err)

	// The default node 1 has a conventional name 'none'.
	assert.Equal(t, "none", name)
}

// Rename a node
func TestNodeRename(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.NodeAdd("buzz", "1.2.3.4:666")
	require.NoError(t, err)
	err = tx.NodeRename("buzz", "rusp")
	require.NoError(t, err)
	node, err := tx.NodeByName("rusp")
	require.NoError(t, err)
	assert.Equal(t, "rusp", node.Name)

	_, err = tx.NodeAdd("buzz", "5.6.7.8:666")
	require.NoError(t, err)
	err = tx.NodeRename("rusp", "buzz")
	assert.Equal(t, db.DbErrAlreadyDefined, err)
}

// Remove a new raft node.
func TestNodeRemove(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.NodeAdd("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	id, err := tx.NodeAdd("rusp", "5.6.7.8:666")
	require.NoError(t, err)

	err = tx.NodeRemove(id)
	require.NoError(t, err)

	_, err = tx.NodeByName("buzz")
	assert.NoError(t, err)

	_, err = tx.NodeByName("rusp")
	assert.Equal(t, db.NoSuchObjectError, err)
}

// Update the heartbeat of a node.
func TestNodeHeartbeat(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.NodeAdd("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	err = tx.NodeHeartbeat("1.2.3.4:666", time.Now().Add(-time.Minute))
	require.NoError(t, err)

	nodes, err := tx.Nodes()
	require.NoError(t, err)
	require.Len(t, nodes, 2)

	node := nodes[1]
	assert.True(t, node.IsDown())
}

// A node is considered empty only if it has no containers and no images.
func TestNodeIsEmpty(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.NodeAdd("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	empty, err := tx.NodeIsEmpty(id)
	require.NoError(t, err)
	assert.True(t, empty)

	_, err = tx.Tx().Exec(`
INSERT INTO containers (id, node_id, name, architecture, type) VALUES (1, ?, 'foo', 1, 1)
`, id)
	require.NoError(t, err)

	empty, err = tx.NodeIsEmpty(id)
	require.NoError(t, err)
	assert.False(t, empty)

	err = tx.NodeClear(id)
	require.NoError(t, err)

	empty, err = tx.NodeIsEmpty(id)
	require.NoError(t, err)
	assert.True(t, empty)
}
