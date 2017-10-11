package node

import "github.com/lxc/lxd/lxd/db"

// DetermineRaftNode figures out what raft node ID and address we have, if any.
//
// This decision is based on the values of the core.https_address config key
// and on the rows in the raft_nodes table, both stored in the node-level
// SQLite database.
//
// The following rules are applied:
//
// - If no core.https_address config key is set, this is a non-clustered node
//   and the returned RaftNode will have ID 1 but no address, to signal that
//   the node should setup an in-memory raft cluster where the node itself
//   is the only member and leader.
//
// - If core.https_address config key is set, but there is no row in the
//   raft_nodes table, this is a non-clustered node as well, and same behavior
//   as the previous case applies.
//
// - If core.https_address config key is set and there is at least one row in
//   the raft_nodes table, then this node is considered a raft node if
//   core.https_address matches one of the rows in raft_nodes. In that case,
//   the matching db.RaftNode row is returned, otherwise nil.
func DetermineRaftNode(tx *db.NodeTx) (*db.RaftNode, error) {
	config, err := ConfigLoad(tx)
	if err != nil {
		return nil, err
	}

	address := config.HTTPSAddress()

	// If core.https_address is the empty string, then this LXD instance is
	// not running in clustering mode.
	if address == "" {
		return &db.RaftNode{ID: 1}, nil
	}

	nodes, err := tx.RaftNodes()
	if err != nil {
		return nil, err
	}

	// If core.https_address is set, but raft_nodes has no rows, this is
	// still an instance not running in clustering mode.
	if len(nodes) == 0 {
		return &db.RaftNode{ID: 1}, nil
	}

	// If there is one or more row in raft_nodes, try to find a matching
	// one.
	for _, node := range nodes {
		if node.Address == address {
			return &node, nil
		}
	}

	return nil, nil
}
