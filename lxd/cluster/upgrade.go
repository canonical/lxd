package cluster

import (
	"fmt"
	"net/http"
	"os"
	"time"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
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

// KeepUpdated is a task that continuously monitor this node's version to see it
// it's out of date with respect to other nodes. In the node is out of date,
// and the LXD_CLUSTER_UPDATE environment variable is set, then the task
// executes the executable that the variable is pointing at.
func KeepUpdated(state *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		ch := make(chan struct{})
		go func() {
			maybeUpdate(state)
			close(ch)
		}()
		select {
		case <-ctx.Done():
		case <-ch:
		}
	}

	schedule := task.Every(5 * time.Minute)

	return f, schedule
}

// Check this node's version and possibly run LXD_CLUSTER_UPDATE.
func maybeUpdate(state *state.State) {
	shouldUpdate := false

	enabled, err := Enabled(state.Node)
	if err != nil {
		logger.Errorf("Failed to check clustering is enabled: %v", err)
		return
	}
	if !enabled {
		return
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
		logger.Errorf("Failed to check if this node is out-of-date: %v", err)
		return
	}

	if !shouldUpdate {
		logger.Debugf("Cluster node is up-to-date")
		return
	}

	logger.Infof("Node is out-of-date with respect to other cluster nodes")

	updateExecutable := os.Getenv("LXD_CLUSTER_UPDATE")
	if updateExecutable == "" {
		logger.Debug("No LXD_CLUSTER_UPDATE variable set, skipping auto-update")
		return
	}

	logger.Infof("Triggering cluster update using: %s", updateExecutable)

	_, err = shared.RunCommand(updateExecutable)
	if err != nil {
		logger.Errorf("Cluster upgrade failed: '%v'", err.Error())
		return
	}
}
