package node

import (
	"github.com/lxc/lxd/lxd/db"
)

// DetermineRaftNode figures out what raft node ID and address we have, if any.
//
// This decision is based on the value of the cluster.https_address config key
// and on the rows in the raft_nodes table, both stored in the node-level
// SQLite database.
//
// The following rules are applied:
//
// - If no cluster.https_address config key is set, this is a non-clustered node
//   and the returned RaftNode will have ID 1 but no address, to signal that
//   the node should setup an in-memory raft cluster where the node itself
//   is the only member and leader.
//
// - If cluster.https_address config key is set, but there is no row in the
//   raft_nodes table, this is a brand new clustered node that is joining a
//   cluster, and same behavior as the previous case applies.
//
// - If cluster.https_address config key is set and there is at least one row
//   in the raft_nodes table, then this node is considered a raft node if
//   cluster.https_address matches one of the rows in raft_nodes. In that case,
//   the matching db.RaftNode row is returned, otherwise nil.
func DetermineRaftNode(tx *db.NodeTx) (*db.RaftNode, error) {
	config, err := ConfigLoad(tx)
	if err != nil {
		return nil, err
	}

	address := config.ClusterAddress()

	// If cluster.https_address is the empty string, then this LXD instance is
	// not running in clustering mode.
	if address == "" {
		return &db.RaftNode{ID: 1}, nil
	}

	nodes, err := tx.GetRaftNodes()
	if err != nil {
		return nil, err
	}

	// If cluster.https_address and the raft_nodes table is not populated,
	// this must be a joining node.
	if len(nodes) == 0 {
		return &db.RaftNode{ID: 1}, nil
	}

	// Try to find a matching node.
	for _, node := range nodes {
		if node.Address == address {
			return &node, nil
		}
	}

	return nil, nil
}
