package db

import (
	"fmt"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/pkg/errors"
)

// RaftNode holds information about a single node in the dqlite raft cluster.
type RaftNode struct {
	ID      int64  // Stable node identifier
	Address string // Network address of the node
}

// RaftNodes returns information about all LXD nodes that are members of the
// dqlite Raft cluster (possibly including the local node). If this LXD
// instance is not running in clustered mode, an empty list is returned.
func (n *NodeTx) RaftNodes() ([]RaftNode, error) {
	nodes := []RaftNode{}
	dest := func(i int) []interface{} {
		nodes = append(nodes, RaftNode{})
		return []interface{}{&nodes[i].ID, &nodes[i].Address}
	}
	stmt, err := n.tx.Prepare("SELECT id, address FROM raft_nodes ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	err = query.SelectObjects(stmt, dest)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to fetch raft nodes")
	}
	return nodes, nil
}

// RaftNodeAddresses returns the addresses of all LXD nodes that are members of
// the dqlite Raft cluster (possibly including the local node). If this LXD
// instance is not running in clustered mode, an empty list is returned.
func (n *NodeTx) RaftNodeAddresses() ([]string, error) {
	return query.SelectStrings(n.tx, "SELECT address FROM raft_nodes")
}

// RaftNodeAddress returns the address of the LXD raft node with the given ID,
// if any matching row exists.
func (n *NodeTx) RaftNodeAddress(id int64) (string, error) {
	stmt := "SELECT address FROM raft_nodes WHERE id=?"
	addresses, err := query.SelectStrings(n.tx, stmt, id)
	if err != nil {
		return "", err
	}
	switch len(addresses) {
	case 0:
		return "", ErrNoSuchObject
	case 1:
		return addresses[0], nil
	default:
		// This should never happen since we have a UNIQUE constraint
		// on the raft_nodes.id column.
		return "", fmt.Errorf("more than one match found")
	}
}

// RaftNodeFirst adds a the first node of the cluster. It ensures that the
// database ID is 1, to match the server ID of first raft log entry.
//
// This method is supposed to be called when there are no rows in raft_nodes,
// and it will replace whatever existing row has ID 1.
func (n *NodeTx) RaftNodeFirst(address string) error {
	columns := []string{"id", "address"}
	values := []interface{}{int64(1), address}
	id, err := query.UpsertObject(n.tx, "raft_nodes", columns, values)
	if err != nil {
		return err
	}
	if id != 1 {
		return fmt.Errorf("could not set raft node ID to 1")
	}
	return nil
}

// RaftNodeAdd adds a node to the current list of LXD nodes that are part of the
// dqlite Raft cluster. It returns the ID of the newly inserted row.
func (n *NodeTx) RaftNodeAdd(address string) (int64, error) {
	columns := []string{"address"}
	values := []interface{}{address}
	return query.UpsertObject(n.tx, "raft_nodes", columns, values)
}

// RaftNodeDelete removes a node from the current list of LXD nodes that are
// part of the dqlite Raft cluster.
func (n *NodeTx) RaftNodeDelete(id int64) error {
	deleted, err := query.DeleteObject(n.tx, "raft_nodes", id)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrNoSuchObject
	}
	return nil
}

// RaftNodesReplace replaces the current list of raft nodes.
func (n *NodeTx) RaftNodesReplace(nodes []RaftNode) error {
	_, err := n.tx.Exec("DELETE FROM raft_nodes")
	if err != nil {
		return err
	}

	columns := []string{"id", "address"}
	for _, node := range nodes {
		values := []interface{}{node.ID, node.Address}
		_, err := query.UpsertObject(n.tx, "raft_nodes", columns, values)
		if err != nil {
			return err
		}
	}
	return nil
}
