package node_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/mpvl/subtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The raft identity (ID and address) of a node depends on the value of
// core.https_address and the entries of the raft_nodes table.
func TestDetermineRaftNode(t *testing.T) {
	cases := []struct {
		title     string
		address   string       // Value of core.https_address
		addresses []string     // Entries in raft_nodes
		node      *db.RaftNode // Expected node value
	}{
		{
			`no core.https_address set`,
			"",
			[]string{},
			&db.RaftNode{ID: 1},
		},
		{
			`core.https_address set and and no raft_nodes rows`,
			"1.2.3.4:8443",
			[]string{},
			&db.RaftNode{ID: 1},
		},
		{
			`core.https_address set and matching the one and only raft_nodes row`,
			"1.2.3.4:8443",
			[]string{"1.2.3.4:8443"},
			&db.RaftNode{ID: 1, Address: "1.2.3.4:8443"},
		},
		{
			`core.https_address set and matching one of many raft_nodes rows`,
			"5.6.7.8:999",
			[]string{"1.2.3.4:666", "5.6.7.8:999"},
			&db.RaftNode{ID: 2, Address: "5.6.7.8:999"},
		},
		{
			`core.https_address set and no matching raft_nodes row`,
			"1.2.3.4:666",
			[]string{"5.6.7.8:999"},
			nil,
		},
	}

	for _, c := range cases {
		subtest.Run(t, c.title, func(t *testing.T) {
			tx, cleanup := db.NewTestNodeTx(t)
			defer cleanup()

			err := tx.UpdateConfig(map[string]string{"core.https_address": c.address})
			require.NoError(t, err)

			for _, address := range c.addresses {
				_, err := tx.RaftNodeAdd(address)
				require.NoError(t, err)
			}

			node, err := node.DetermineRaftNode(tx)
			require.NoError(t, err)
			if c.node == nil {
				assert.Nil(t, node)
			} else {
				assert.Equal(t, c.node.ID, node.ID)
				assert.Equal(t, c.node.Address, node.Address)
			}
		})
	}
}
