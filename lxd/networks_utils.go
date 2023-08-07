package main

import (
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/network"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/logger"
)

var networkOVNChassis *bool

// Attaches a network interface to its corresponding network in the cluster's database if found.
func networkAutoAttach(cluster *db.Cluster, devName string) error {
	_, dbInfo, err := cluster.GetNetworkWithInterface(devName)
	if err != nil {
		// No match found, move on
		return nil
	}

	return network.AttachInterface(dbInfo.Name, devName)
}

// networkUpdateForkdnsServersTask runs every 30s and refreshes the forkdns servers list.
func networkUpdateForkdnsServersTask(s *state.State, heartbeatData *cluster.APIHeartbeat) error {
	logger.Debug("Refreshing forkdns servers")

	// Use project.Default here as forkdns (fan bridge) networks don't support projects.
	projectName := project.Default

	// Get a list of managed networks
	networks, err := s.DB.Cluster.GetCreatedNetworks(projectName)
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
