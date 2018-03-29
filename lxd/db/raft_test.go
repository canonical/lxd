package db_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fetch all raft nodes.
func TestRaftNodes(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	id1, err := tx.RaftNodeAdd("1.2.3.4:666")
	require.NoError(t, err)

	id2, err := tx.RaftNodeAdd("5.6.7.8:666")
	require.NoError(t, err)

	nodes, err := tx.RaftNodes()
	require.NoError(t, err)

	assert.Equal(t, id1, nodes[0].ID)
	assert.Equal(t, id2, nodes[1].ID)
	assert.Equal(t, "1.2.3.4:666", nodes[0].Address)
	assert.Equal(t, "5.6.7.8:666", nodes[1].Address)
}

// Fetch the addresses of all raft nodes.
func TestRaftNodeAddresses(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	_, err := tx.RaftNodeAdd("1.2.3.4:666")
	require.NoError(t, err)

	_, err = tx.RaftNodeAdd("5.6.7.8:666")
	require.NoError(t, err)

	addresses, err := tx.RaftNodeAddresses()
	require.NoError(t, err)

	assert.Equal(t, []string{"1.2.3.4:666", "5.6.7.8:666"}, addresses)
}

// Fetch the address of the raft node with the given ID.
func TestRaftNodeAddress(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	_, err := tx.RaftNodeAdd("1.2.3.4:666")
	require.NoError(t, err)

	id, err := tx.RaftNodeAdd("5.6.7.8:666")
	require.NoError(t, err)

	address, err := tx.RaftNodeAddress(id)
	require.NoError(t, err)
	assert.Equal(t, "5.6.7.8:666", address)
}

// Add the first raft node.
func TestRaftNodeFirst(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	err := tx.RaftNodeFirst("1.2.3.4:666")
	assert.NoError(t, err)

	err = tx.RaftNodeDelete(1)
	assert.NoError(t, err)

	err = tx.RaftNodeFirst("5.6.7.8:666")
	assert.NoError(t, err)

	address, err := tx.RaftNodeAddress(1)
	require.NoError(t, err)
	assert.Equal(t, "5.6.7.8:666", address)
}

// Add a new raft node.
func TestRaftNodeAdd(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	id, err := tx.RaftNodeAdd("1.2.3.4:666")
	assert.Equal(t, int64(1), id)
	assert.NoError(t, err)
}

// Delete an existing raft node.
func TestRaftNodeDelete(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	id, err := tx.RaftNodeAdd("1.2.3.4:666")
	require.NoError(t, err)

	err = tx.RaftNodeDelete(id)
	assert.NoError(t, err)
}

// Delete a non-existing raft node returns an error.
func TestRaftNodeDelete_NonExisting(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	err := tx.RaftNodeDelete(1)
	assert.Equal(t, db.ErrNoSuchObject, err)
}

// Replace all existing raft nodes.
func TestRaftNodesReplace(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	_, err := tx.RaftNodeAdd("1.2.3.4:666")
	require.NoError(t, err)

	nodes := []db.RaftNode{
		{ID: 2, Address: "2.2.2.2:666"},
		{ID: 3, Address: "3.3.3.3:666"},
	}
	err = tx.RaftNodesReplace(nodes)
	assert.NoError(t, err)

	newNodes, err := tx.RaftNodes()
	require.NoError(t, err)

	assert.Equal(t, nodes, newNodes)
}
