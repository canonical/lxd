// +build linux,cgo,!agent

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

	id1, err := tx.CreateRaftNode("1.2.3.4:666")
	require.NoError(t, err)

	id2, err := tx.CreateRaftNode("5.6.7.8:666")
	require.NoError(t, err)

	nodes, err := tx.GetRaftNodes()
	require.NoError(t, err)

	assert.Equal(t, uint64(id1), nodes[0].ID)
	assert.Equal(t, uint64(id2), nodes[1].ID)
	assert.Equal(t, "1.2.3.4:666", nodes[0].Address)
	assert.Equal(t, "5.6.7.8:666", nodes[1].Address)
}

// Fetch the addresses of all raft nodes.
func TestGetRaftNodeAddresses(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	_, err := tx.CreateRaftNode("1.2.3.4:666")
	require.NoError(t, err)

	_, err = tx.CreateRaftNode("5.6.7.8:666")
	require.NoError(t, err)

	addresses, err := tx.GetRaftNodeAddresses()
	require.NoError(t, err)

	assert.Equal(t, []string{"1.2.3.4:666", "5.6.7.8:666"}, addresses)
}

// Fetch the address of the raft node with the given ID.
func TestGetRaftNodeAddress(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	_, err := tx.CreateRaftNode("1.2.3.4:666")
	require.NoError(t, err)

	id, err := tx.CreateRaftNode("5.6.7.8:666")
	require.NoError(t, err)

	address, err := tx.GetRaftNodeAddress(id)
	require.NoError(t, err)
	assert.Equal(t, "5.6.7.8:666", address)
}

// Add the first raft node.
func TestCreateFirstRaftNode(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	err := tx.CreateFirstRaftNode("1.2.3.4:666")
	assert.NoError(t, err)

	err = tx.RemoteRaftNode(1)
	assert.NoError(t, err)

	err = tx.CreateFirstRaftNode("5.6.7.8:666")
	assert.NoError(t, err)

	address, err := tx.GetRaftNodeAddress(1)
	require.NoError(t, err)
	assert.Equal(t, "5.6.7.8:666", address)
}

// Add a new raft node.
func TestCreateRaftNode(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	id, err := tx.CreateRaftNode("1.2.3.4:666")
	assert.Equal(t, int64(1), id)
	assert.NoError(t, err)
}

// Delete an existing raft node.
func TestRemoteRaftNode(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	id, err := tx.CreateRaftNode("1.2.3.4:666")
	require.NoError(t, err)

	err = tx.RemoteRaftNode(id)
	assert.NoError(t, err)
}

// Delete a non-existing raft node returns an error.
func TestRemoteRaftNode_NonExisting(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	err := tx.RemoteRaftNode(1)
	assert.Equal(t, db.ErrNoSuchObject, err)
}

// Replace all existing raft nodes.
func TestReplaceRaftNodes(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	_, err := tx.CreateRaftNode("1.2.3.4:666")
	require.NoError(t, err)

	nodes := []db.RaftNode{
		{ID: 2, Address: "2.2.2.2:666"},
		{ID: 3, Address: "3.3.3.3:666"},
	}
	err = tx.ReplaceRaftNodes(nodes)
	assert.NoError(t, err)

	newNodes, err := tx.GetRaftNodes()
	require.NoError(t, err)

	assert.Equal(t, nodes, newNodes)
}
