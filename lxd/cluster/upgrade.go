package cluster

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"time"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

// NotifyUpgradeCompleted sends a notification to all other nodes in the
// cluster that any possible pending database update has been applied, and any
// nodes which was waiting for this node to be upgraded should re-check if it's
// okay to move forward.
func NotifyUpgradeCompleted(state *state.State, cert *shared.CertInfo) error {
	notifier, err := NewNotifier(state, cert, NotifyTryAll)
	if err != nil {
		return err
	}

	return notifier(func(client lxd.InstanceServer) error {
		info, err := client.GetConnectionInfo()
		if err != nil {
			return errors.Wrap(err, "failed to get connection info")
		}

		url := fmt.Sprintf("%s%s", info.Addresses[0], databaseEndpoint)
		request, err := http.NewRequest("PATCH", url, nil)
		if err != nil {
			return errors.Wrap(err, "failed to create database notify upgrade request")
		}
		setDqliteVersionHeader(request)

		httpClient, err := client.GetHTTPClient()
		if err != nil {
			return errors.Wrap(err, "failed to get HTTP client")
		}

		httpClient.Timeout = 5 * time.Second
		response, err := httpClient.Do(request)
		if err != nil {
			return errors.Wrap(err, "failed to notify node about completed upgrade")
		}

		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("database upgrade notification failed: %s", response.Status)
		}

		return nil
	})
}

// MaybeUpdate Check this node's version and possibly run LXD_CLUSTER_UPDATE.
func MaybeUpdate(state *state.State) error {
	shouldUpdate := false

	enabled, err := Enabled(state.Node)
	if err != nil {
		return errors.Wrap(err, "Failed to check clustering is enabled")
	}
	if !enabled {
		return nil
	}

	if state.Cluster == nil {
		return fmt.Errorf("Failed checking cluster update, state not initialised yet")
	}

	err = state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		outdated, err := tx.NodeIsOutdated()
		if err != nil {
			return err
		}
		shouldUpdate = outdated
		return nil
	})

	if err != nil {
		// Just log the error and return.
		return errors.Wrap(err, "Failed to check if this node is out-of-date")
	}

	if !shouldUpdate {
		logger.Debugf("Cluster node is up-to-date")
		return nil
	}

	return triggerUpdate()
}

func triggerUpdate() error {
	logger.Infof("Node is out-of-date with respect to other cluster nodes")

	updateExecutable := os.Getenv("LXD_CLUSTER_UPDATE")
	if updateExecutable == "" {
		logger.Debug("No LXD_CLUSTER_UPDATE variable set, skipping auto-update")
		return nil
	}

	// Wait a random amout of seconds (up to 30) in order to avoid
	// restarting all cluster members at the same time, and make the
	// upgrade more graceful.
	wait := time.Duration(rand.Intn(30)) * time.Second
	logger.Infof("Triggering cluster update in %s using: %s", wait, updateExecutable)
	time.Sleep(wait)

	_, err := shared.RunCommand(updateExecutable)
	if err != nil {
		logger.Errorf("Cluster upgrade failed: '%v'", err.Error())
		return err
	}
	return nil
}

// UpgradeMembersWithoutRole assigns the Spare raft role to all cluster members
// that are not currently part of the raft configuration. It's used for
// upgrading a cluster from a version without roles support.
func UpgradeMembersWithoutRole(gateway *Gateway, members []db.NodeInfo) error {
	nodes, err := gateway.currentRaftNodes()
	if err == ErrNotLeader {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "Failed to get current raft nodes")
	}

	// Used raft IDs.
	ids := map[uint64]bool{}
	for _, node := range nodes {
		ids[node.ID] = true
	}

	client, err := gateway.getClient()
	if err != nil {
		return errors.Wrap(err, "Failed to connect to local dqlite node")
	}
	defer client.Close()

	// Check that each member is present in the raft configuration, and add
	// it if not.
	for _, member := range members {
		found := false
		for _, node := range nodes {
			if member.ID == 1 && node.ID == 1 || member.Address == node.Address {
				found = true
				break
			}
		}
		if found {
			continue
		}

		// Try to use the same ID as the node, but it might not be
		// possible if it's use.
		id := uint64(member.ID)
		if _, ok := ids[id]; ok {
			for _, other := range members {
				if _, ok := ids[uint64(other.ID)]; !ok {
					id = uint64(other.ID)
					break
				}
			}
			// Sanity check: can't really happen since there are
			// always at least as many members as there are nodes,
			// and all of them have different IDs.
			if id == uint64(member.ID) {
				panic("no available ID")
			}
		}
		ids[id] = true

		info := db.RaftNode{
			ID:      id,
			Address: member.Address,
			Role:    db.RaftSpare,
		}

		logger.Info("Add spare dqlite node", log15.Ctx{"id": info.ID, "address": info.Address})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = client.Add(ctx, info)
		if err != nil {
			return errors.Wrap(err, "Failed to add dqlite node")
		}

	}

	return nil
}
