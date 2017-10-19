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
	assert.Equal(t, int64(1), id)

	nodes, err := tx.Nodes()
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	node := nodes[0]
	assert.Equal(t, "buzz", node.Name)
	assert.Equal(t, "1.2.3.4:666", node.Address)
	assert.Equal(t, cluster.SchemaVersion, node.Schema)
	assert.Equal(t, len(version.APIExtensions), node.APIExtensions)
	assert.False(t, node.IsDown())
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
	require.Len(t, nodes, 1)

	node := nodes[0]
	assert.True(t, node.IsDown())
}
