package network

import (
	"fmt"
	"net"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/validate"
)

// physical represents a LXD physical network.
type physical struct {
	common
}

// Type returns the network type.
func (n *physical) Type() string {
	return "physical"
}

// DBType returns the network type DB ID.
func (n *physical) DBType() db.NetworkType {
	return db.NetworkTypePhysical
}

// Validate network config.
func (n *physical) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		"parent":                      validate.Required(validate.IsNotEmpty, validInterfaceName),
		"mtu":                         validate.Optional(validate.IsNetworkMTU),
		"vlan":                        validate.Optional(validate.IsNetworkVLAN),
		"maas.subnet.ipv4":            validate.IsAny,
		"maas.subnet.ipv6":            validate.IsAny,
		"ipv4.gateway":                validate.Optional(validate.IsNetworkAddressCIDRV4),
		"ipv6.gateway":                validate.Optional(validate.IsNetworkAddressCIDRV6),
		"ipv4.ovn.ranges":             validate.Optional(validate.IsNetworkRangeV4List),
		"ipv6.ovn.ranges":             validate.Optional(validate.IsNetworkRangeV6List),
		"dns.nameservers":             validate.Optional(validate.IsNetworkAddressList),
		"volatile.last_state.created": validate.Optional(validate.IsBool),
	}

	err := n.validate(config, rules)
	if err != nil {
		return err
	}

	return nil
}

// checkParentUse checks if parent is already in use by another network or instance device.
func (n *physical) checkParentUse(ourConfig map[string]string) (bool, error) {
	// Get all managed networks across all projects.
	var err error
	var projectNetworks map[string]map[int64]api.Network

	err = n.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		projectNetworks, err = tx.GetNonPendingNetworks()
		return err
	})
	if err != nil {
		return false, errors.Wrapf(err, "Failed to load all networks")
	}

	for projectName, networks := range projectNetworks {
		if projectName != project.Default {
			continue // Only default project networks can possibly reference a physical interface.
		}

		for _, network := range networks {
			if network.Name == n.name {
				continue // Ignore our own DB record.
			}

			// Check if another network is using our parent.
			if network.Config["parent"] == ourConfig["parent"] {
				// If either network doesn't specify a vlan, or both specify same vlan,
				// then we can't use this parent.
				if (network.Config["vlan"] == "" || ourConfig["vlan"] == "") || network.Config["vlan"] == ourConfig["vlan"] {
					return true, nil
				}
			}
		}
	}

	return false, nil
}

// Create checks whether the referenced parent interface is used by other networks or instance devices, as we
// need to have exclusive access to the interface.
func (n *physical) Create(clientType cluster.ClientType) error {
	n.logger.Debug("Create", log.Ctx{"clientType": clientType, "config": n.config})

	// We only need to check in the database once, not on every clustered node.
	if clientType == cluster.ClientTypeNormal {
		inUse, err := n.checkParentUse(n.config)
		if err != nil {
			return err
		}
		if inUse {
			return fmt.Errorf("Parent interface %q in use by another network", n.config["parent"])
		}
	}

	return nil
}

// Delete deletes a network.
func (n *physical) Delete(clientType cluster.ClientType) error {
	n.logger.Debug("Delete", log.Ctx{"clientType": clientType})

	err := n.Stop()
	if err != nil {
		return err
	}

	return n.common.delete(clientType)
}

// Rename renames a network.
func (n *physical) Rename(newName string) error {
	n.logger.Debug("Rename", log.Ctx{"newName": newName})

	// Rename common steps.
	err := n.common.rename(newName)
	if err != nil {
		return err
	}

	return nil
}

// Start starts is a no-op.
func (n *physical) Start() error {
	n.logger.Debug("Start")

	if n.status == api.NetworkStatusPending {
		return fmt.Errorf("Cannot start pending network")
	}

	revert := revert.New()
	defer revert.Fail()

	hostName := GetHostDevice(n.config["parent"], n.config["vlan"])
	created, err := VLANInterfaceCreate(n.config["parent"], hostName, n.config["vlan"])
	if err != nil {
		return err
	}
	if created {
		revert.Add(func() { InterfaceRemove(hostName) })
	}

	// Set the MTU.
	if n.config["mtu"] != "" {
		err = InterfaceSetMTU(hostName, n.config["mtu"])
		if err != nil {
			return err
		}
	}

	// Record if we created this device or not (if we have not already recorded that we created it previously),
	// so it can be removed on stop. This way we won't overwrite the setting on LXD restart.
	if !shared.IsTrue(n.config["volatile.last_state.created"]) {
		n.config["volatile.last_state.created"] = fmt.Sprintf("%t", created)
		err = n.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
			return tx.UpdateNetwork(n.id, n.description, n.config)
		})
		if err != nil {
			return errors.Wrapf(err, "Failed saving volatile config")
		}
	}

	revert.Success()
	return nil
}

// Stop stops is a no-op.
func (n *physical) Stop() error {
	n.logger.Debug("Stop")

	hostName := GetHostDevice(n.config["parent"], n.config["vlan"])

	// Only try and remove created VLAN interfaces.
	if n.config["vlan"] != "" && shared.IsTrue(n.config["volatile.last_state.created"]) && InterfaceExists(hostName) {
		err := InterfaceRemove(hostName)
		if err != nil {
			return err
		}
	}

	// Reset MTU back to 1500 if overridden in config.
	if n.config["mtu"] != "" && InterfaceExists(hostName) {
		err := InterfaceSetMTU(hostName, "1500")
		if err != nil {
			return err
		}
	}

	// Remove last state config.
	delete(n.config, "volatile.last_state.created")
	err := n.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		return tx.UpdateNetwork(n.id, n.description, n.config)
	})
	if err != nil {
		return errors.Wrapf(err, "Failed removing volatile config")
	}

	return nil
}

// Update updates the network. Accepts notification boolean indicating if this update request is coming from a
// cluster notification, in which case do not update the database, just apply local changes needed.
func (n *physical) Update(newNetwork api.NetworkPut, targetNode string, clientType cluster.ClientType) error {
	n.logger.Debug("Update", log.Ctx{"clientType": clientType, "newNetwork": newNetwork})

	dbUpdateNeeeded, changedKeys, oldNetwork, err := n.common.configChanged(newNetwork)
	if err != nil {
		return err
	}

	if !dbUpdateNeeeded {
		return nil // Nothing changed.
	}

	revert := revert.New()
	defer revert.Fail()

	hostNameChanged := shared.StringInSlice("vlan", changedKeys) || shared.StringInSlice("parent", changedKeys)

	// We only need to check in the database once, not on every clustered node.
	if clientType == cluster.ClientTypeNormal {
		if hostNameChanged {
			isUsed, err := n.IsUsed()
			if isUsed || err != nil {
				return fmt.Errorf("Cannot update network host name when in use")
			}

			inUse, err := n.checkParentUse(newNetwork.Config)
			if err != nil {
				return err
			}
			if inUse {
				return fmt.Errorf("Parent interface %q in use by another network", newNetwork.Config["parent"])
			}
		}
	}

	if hostNameChanged {
		err = n.Stop()
		if err != nil {
			return err
		}

		// Remove the volatile last state from submitted new config if present.
		delete(newNetwork.Config, "volatile.last_state.created")
	}

	// Define a function which reverts everything.
	revert.Add(func() {
		// Reset changes to all nodes and database.
		n.common.update(oldNetwork, targetNode, clientType)
	})

	// Apply changes to all nodes and databse.
	err = n.common.update(newNetwork, targetNode, clientType)
	if err != nil {
		return err
	}

	err = n.Start()
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// DHCPv4Subnet returns the DHCPv4 subnet (if DHCP is enabled on network).
func (n *physical) DHCPv4Subnet() *net.IPNet {
	_, subnet, err := net.ParseCIDR(n.config["ipv4.gateway"])
	if err != nil {
		return nil
	}

	return subnet
}

// DHCPv6Subnet returns the DHCPv6 subnet (if DHCP or SLAAC is enabled on network).
func (n *physical) DHCPv6Subnet() *net.IPNet {
	_, subnet, err := net.ParseCIDR(n.config["ipv6.gateway"])
	if err != nil {
		return nil
	}

	return subnet
}
