package cluster

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	dqlite "github.com/canonical/go-dqlite"
	client "github.com/canonical/go-dqlite/client"
	"github.com/grant-he/lxd/lxd/db"
	"github.com/grant-he/lxd/lxd/node"
	"github.com/pkg/errors"
)

// ListDatabaseNodes returns a list of database node names.
func ListDatabaseNodes(database *db.Node) ([]string, error) {
	nodes := []db.RaftNode{}
	err := database.Transaction(func(tx *db.NodeTx) error {
		var err error
		nodes, err = tx.GetRaftNodes()
		return err
	})
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to list database nodes")
	}
	addresses := make([]string, 0)
	for _, node := range nodes {
		if node.Role != db.RaftVoter {
			continue
		}
		addresses = append(addresses, node.Address)
	}
	return addresses, nil
}

// Recover attempts data recovery on the cluster database.
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
		return tx.ReplaceRaftNodes(nodes)
	})
	if err != nil {
		return errors.Wrap(err, "Failed to update database nodes")
	}

	return nil
}

// RemoveRaftNode removes a raft node from the raft configuration.
func RemoveRaftNode(gateway *Gateway, address string) error {
	nodes, err := gateway.currentRaftNodes()
	if err != nil {
		return errors.Wrap(err, "Failed to get current raft nodes")
	}
	var id uint64
	for _, node := range nodes {
		if node.Address == address {
			id = node.ID
			break
		}
	}
	if id == 0 {
		return fmt.Errorf("No raft node with address %q", address)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := client.FindLeader(
		ctx, gateway.NodeStore(),
		client.WithDialFunc(gateway.raftDial()),
		client.WithLogFunc(DqliteLog),
	)
	if err != nil {
		return errors.Wrap(err, "Failed to connect to cluster leader")
	}
	defer client.Close()
	err = client.Remove(ctx, id)
	if err != nil {
		return errors.Wrap(err, "Failed to remove node")
	}
	return nil
}
