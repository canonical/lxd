package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/network"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

var networkOVNChassis *bool

func networkAutoAttach(cluster *db.Cluster, devName string) error {
	var networkName string
	_ = cluster.Transaction(context.TODO(), func(ctx context.Context, c *db.ClusterTx) error {
		_, dbInfo, err := c.GetNetworkWithInterface(ctx, devName)
		if err != nil {
			if !api.StatusErrorCheck(err, http.StatusNotFound) {
				return fmt.Errorf("Failed finding network matching interface %q: %w", devName, err)
			}

			return nil // No match found, move on.
		}

		networkName = dbInfo.Name
		return nil
	})

	if networkName != "" {
		return network.AttachInterface(networkName, devName)
	}

	return nil
}

// networkUpdateForkdnsServersTask runs every 30s and refreshes the forkdns servers list.
func networkUpdateForkdnsServersTask(s *state.State, heartbeatData *cluster.APIHeartbeat) error {
	logger.Debug("Refreshing forkdns servers")

	// Use api.ProjectDefaultName here as forkdns (fan bridge) networks don't support projects.
	projectName := api.ProjectDefaultName

	// Get a list of managed networks
	var networks []string
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, c *db.ClusterTx) error {
		var err error
		networks, err = c.GetCreatedNetworkNamesByProject(ctx, projectName)

		return err
	})
	if err != nil {
		return err
	}

	for _, name := range networks {
		n, err := network.LoadByName(s, projectName, name)
		if err != nil {
			logger.Errorf("Failed to load network %q from project %q for heartbeat", name, projectName)
			continue
		}

		if n.Type() == "bridge" && n.Config()["bridge.mode"] == "fan" {
			err := n.HandleHeartbeat(heartbeatData)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// networkUpdateOVNChassis gets called on heartbeats to check if OVN needs reconfiguring.
func networkUpdateOVNChassis(s *state.State, heartbeatData *cluster.APIHeartbeat, localAddress string) error {
	// Check if we have at least one active OVN chassis.
	hasOVNChassis := false
	localOVNChassis := false
	for _, n := range heartbeatData.Members {
		for _, role := range n.Roles {
			if role == db.ClusterRoleOVNChassis {
				if n.Address == localAddress {
					localOVNChassis = true
				}

				hasOVNChassis = true
				break
			}
		}
	}

	runChassis := !hasOVNChassis || localOVNChassis
	if networkOVNChassis != nil && *networkOVNChassis != runChassis {
		// Detected that the local OVN chassis setup may be incorrect, restarting.
		err := networkRestartOVN(s)
		if err != nil {
			logger.Error("Error restarting OVN networks", logger.Ctx{"err": err})
		}
	}

	networkOVNChassis = &runChassis
	return nil
}
