package acl

import (
	"fmt"
	"net"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

// FirewallEnsureACLs configures the firewall with ACLs and baseline rules for the network. If no ACLs are in use
// by the network or its instance NICs then any existing rules are removed from the network.
func FirewallEnsureACLs(s *state.State, logger logger.Logger, networkProjectName string, networkName string, intRouterIPs []*net.IPNet, dnsIPs []net.IP, ignoreUsageType interface{}, ignoreUsageNicName string, keepACLs ...string) error {
	var err error

	aclNames := keepACLs

	// Find instances using the specified network and build a list of ACLs being applied.
	err = s.Cluster.InstanceList(nil, func(inst db.Instance, p api.Project, profiles []api.Profile) error {
		ignoreInst, isIgnoreInst := ignoreUsageType.(instance.Instance)

		if isIgnoreInst && ignoreUsageNicName == "" {
			return fmt.Errorf("ignoreUsageNicName should be specified when providing an instance in ignoreUsageType")
		}

		// Get the instance's effective network project name.
		instNetworkProject := project.NetworkProjectFromRecord(&p)

		// Skip instances who's effective network project doesn't match this Network's project.
		if instNetworkProject != networkProjectName {
			return nil
		}

		devices := db.ExpandInstanceDevices(deviceConfig.NewDevices(inst.Devices), profiles)

		// Iterate through each of the instance's devices, looking for NICs connected to this network using
		// ACLs.
		for devName, devConfig := range devices {
			// Only interested in NICs linked to this managed network.
			if devConfig["type"] != "nic" || devConfig["network"] != networkName {
				continue
			}

			// If an ignore instance was provided, then skip the device that the ACLs were just removed
			// from. In case DB record is not updated until the update process has completed otherwise
			// we would still consider it using the ACL.
			if isIgnoreInst && ignoreInst.Name() == inst.Name && ignoreInst.Project() == inst.Project && ignoreUsageNicName == devName {
				continue
			}

			for _, nicACLName := range util.SplitNTrimSpace(devConfig["security.acls"], ",", -1, true) {
				if !shared.StringInSlice(nicACLName, aclNames) {
					aclNames = append(aclNames, nicACLName)
				}
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Load applied ACLs.
	acls := make(map[int64]*api.NetworkACL, len(aclNames))
	for _, aclName := range aclNames {
		aclID, aclInfo, err := s.Cluster.GetNetworkACL(networkProjectName, aclName)
		if err != nil {
			return errors.Wrapf(err, "Failed loading ACL %q", aclName)
		}

		acls[aclID] = aclInfo
	}

	if len(acls) > 0 {
		logger.Debug("Applying ACLs to network", log.Ctx{"networkACLs": aclNames})
	} else {
		logger.Debug("Clearing ACLs from network")
	}

	// Apply baseline and ACL rules (if no ACLs supplied then any active config will be removed).
	err = s.Firewall.ACLNetworkSetup(networkName, intRouterIPs, dnsIPs, acls)
	if err != nil {
		return err
	}

	return nil
}
