package cluster

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/grant-he/lxd/lxd/db"
	"github.com/grant-he/lxd/lxd/node"
	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/log15"
	"github.com/grant-he/lxd/shared/logger"
	"github.com/pkg/errors"
)

// Load information about the dqlite node associated with this LXD member
// should have, such as its ID, address and role.
func loadInfo(database *db.Node, cert *shared.CertInfo) (*db.RaftNode, error) {
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
	logger.Debug("Start database node", log15.Ctx{"id": info.ID, "address": info.Address, "role": info.Role})

	if info.Address == "" {
		// This is a standalone node not exposed to the network.
		info.Address = "1"
	}

	// Rename legacy data directory if needed.
	dir := filepath.Join(database.Dir(), "global")
	legacyDir := filepath.Join(database.Dir(), "..", "raft")
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

	return info, nil
}
