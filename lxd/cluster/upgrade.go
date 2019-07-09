package cluster

import (
	"context"
	"fmt"
	"net/http"
	"os"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

// NotifyUpgradeCompleted sends a notification to all other nodes in the
// cluster that any possible pending database update has been applied, and any
// nodes which was waiting for this node to be upgraded should re-check if it's
// okay to move forward.
func NotifyUpgradeCompleted(state *state.State, cert *shared.CertInfo) error {
	notifier, err := NewNotifier(state, cert, NotifyAll)
	if err != nil {
		return err
	}
	return notifier(func(client lxd.ContainerServer) error {
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

	logger.Infof("Triggering cluster update using: %s", updateExecutable)

	_, err := shared.RunCommand(updateExecutable)
	if err != nil {
		logger.Errorf("Cluster upgrade failed: '%v'", err.Error())
		return err
	}
	return nil
}
