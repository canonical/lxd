package cluster

import (
	"fmt"
	"os"
	"path/filepath"

	client "github.com/canonical/go-dqlite/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

// Create a raft instance and all its dependencies, to be used as backend for
// the dqlite driver running on this LXD node.
//
// If this node should not serve as dqlite node, nil is returned.
//
// The raft instance will use an in-memory transport if clustering is not
// enabled on this node.
//
// The certInfo parameter should contain the cluster TLS keypair and optional
// CA certificate.
//
// The latency parameter is a coarse grain measure of how fast/reliable network
// links are. This is used to tweak the various timeouts parameters of the raft
// algorithm. See the raft.Config structure for more details. A value of 1.0
// means use the default values from hashicorp's raft package. Values closer to
// 0 reduce the values of the various timeouts (useful when running unit tests
// in-memory).
func newRaft(database *db.Node, cert *shared.CertInfo, latency float64) (*raftInstance, error) {
	if latency <= 0 {
		return nil, fmt.Errorf("latency should be positive")
	}

	// Figure out if we actually need to act as dqlite node.
	var info *db.RaftNode
	err := database.Transaction(func(tx *db.NodeTx) error {
		var err error
		info, err = node.DetermineRaftNode(tx)
		return err
	})
	if err != nil {
		return nil, err
	}

	// If we're not part of the dqlite cluster, there's nothing to do.
	if info == nil {
		return nil, nil
	}
	logger.Debug("Start database node", log15.Ctx{"id": info.ID, "address": info.Address})

	// Initialize a raft instance along with all needed dependencies.
	instance, err := raftInstanceInit(database, info, cert, latency)
	if err != nil {
		return nil, err
	}

	return instance, nil
}

// A LXD-specific wrapper around raft.Raft, which also holds a reference to its
// network transport and dqlite FSM.
type raftInstance struct {
	info client.NodeInfo
}

// Create a new raftFactory, instantiating all needed raft dependencies.
func raftInstanceInit(
	db *db.Node, node *db.RaftNode, cert *shared.CertInfo, latency float64) (*raftInstance, error) {

	addr := node.Address
	if addr == "" {
		// This is a standalone node not exposed to the network.
		addr = "1"
	}

	// Rename legacy data directory if needed.
	dir := filepath.Join(db.Dir(), "global")
	legacyDir := filepath.Join(db.Dir(), "..", "raft")
	if shared.PathExists(legacyDir) {
		if shared.PathExists(dir) {
			return nil, fmt.Errorf("both legacy and new global database directories exist")
		}
		logger.Info("Renaming global database directory from raft/ to database/global/")
		err := os.Rename(legacyDir, dir)
		if err != nil {
			return nil, errors.Wrap(err, "failed to rename legacy global database directory")
		}
	}

	// Data directory
	if !shared.PathExists(dir) {
		err := os.Mkdir(dir, 0750)
		if err != nil {
			return nil, err
		}
	}

	instance := &raftInstance{}
	instance.info.ID = uint64(node.ID)
	instance.info.Address = addr

	return instance, nil
}

// An address provider that looks up server addresses in the raft_nodes table.
type raftAddressProvider struct {
	db *db.Node
}

func (p *raftAddressProvider) ServerAddr(databaseID int) (string, error) {
	var address string
	err := p.db.Transaction(func(tx *db.NodeTx) error {
		var err error
		address, err = tx.RaftNodeAddress(int64(databaseID))
		return err
	})
	if err != nil {
		return "", err
	}
	return address, nil
}
