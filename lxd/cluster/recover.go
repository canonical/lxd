package cluster

import (
	"fmt"
	"path/filepath"

	dqlite "github.com/canonical/go-dqlite"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/pkg/errors"
)

func ListDatabaseNodes(database *db.Node) ([]string, error) {
	nodes := []db.RaftNode{}
	err := database.Transaction(func(tx *db.NodeTx) error {
		var err error
		nodes, err = tx.RaftNodes()
		return err
	})
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to list database nodes")
	}
	addresses := make([]string, len(nodes))
	for i, node := range nodes {
		addresses[i] = node.Address
	}
	return addresses, nil
}

func Recover(database *db.Node) error {
	// Figure out if we actually act as dqlite node.
	var info *db.RaftNode
	err := database.Transaction(func(tx *db.NodeTx) error {
		var err error
		info, err = node.DetermineRaftNode(tx)
		return err
	})
	if err != nil {
		return errors.Wrap(err, "Failed to determine node role")
	}

	// If we're not a database node, return an error.
	if info == nil {
		return fmt.Errorf("This LXD instance has no database role")
	}

	// If this is a standalone node not exposed to the network, return an
	// error.
	if info.Address == "" {
		return fmt.Errorf("This LXD instance is not clustered")
	}

	dir := filepath.Join(database.Dir(), "global")
	server, err := dqlite.New(
		uint64(info.ID),
		info.Address,
		dir,
	)
	if err != nil {
		return errors.Wrap(err, "Failed to create dqlite server")
	}

	cluster := []dqlite.NodeInfo{
		{ID: uint64(info.ID), Address: info.Address},
	}

	err = server.Recover(cluster)
	if err != nil {
		return errors.Wrap(err, "Failed to recover database state")
	}

	// Update the list of raft nodes.
	err = database.Transaction(func(tx *db.NodeTx) error {
		nodes := []db.RaftNode{
			{ID: info.ID, Address: info.Address},
		}
		return tx.RaftNodesReplace(nodes)
	})
	if err != nil {
		return errors.Wrap(err, "Failed to update database nodes")
	}

	return nil
}
