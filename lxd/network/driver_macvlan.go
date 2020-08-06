package network

import (
	"fmt"

	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/validate"
)

// macvlan represents a LXD macvlan network.
type macvlan struct {
	common
}

// Validate network config.
func (n *macvlan) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		"parent":           validInterfaceName,
		"mtu":              validate.Optional(validate.IsInt64),
		"vlan":             validate.Optional(validate.IsNetworkVLAN),
		"maas.subnet.ipv4": validate.IsAny,
		"maas.subnet.ipv6": validate.IsAny,
	}

	err := n.validate(config, rules)
	if err != nil {
		return err
	}

	return nil
}

// Delete deletes a network.
func (n *macvlan) Delete(clusterNotification bool) error {
	n.logger.Debug("Delete", log.Ctx{"clusterNotification": clusterNotification})
	return n.common.delete(clusterNotification)
}

// Rename renames a network.
func (n *macvlan) Rename(newName string) error {
	n.logger.Debug("Rename", log.Ctx{"newName": newName})

	// Sanity checks.
	inUse, err := n.IsUsed()
	if err != nil {
		return err
	}

	if inUse {
		return fmt.Errorf("The network is currently in use")
	}

	// Rename common steps.
	err = n.common.rename(newName)
	if err != nil {
		return err
	}

	return nil
}

// Start starts is a no-op.
func (n *macvlan) Start() error {
	if n.status == api.NetworkStatusPending {
		return fmt.Errorf("Cannot start pending network")
	}

	return nil
}

// Stop stops is a no-op.
func (n *macvlan) Stop() error {
	return nil
}

// Update updates the network. Accepts notification boolean indicating if this update request is coming from a
// cluster notification, in which case do not update the database, just apply local changes needed.
func (n *macvlan) Update(newNetwork api.NetworkPut, targetNode string, clusterNotification bool) error {
	n.logger.Debug("Update", log.Ctx{"clusterNotification": clusterNotification, "newNetwork": newNetwork})

	dbUpdateNeeeded, _, oldNetwork, err := n.common.configChanged(newNetwork)
	if err != nil {
		return err
	}

	if !dbUpdateNeeeded {
		return nil // Nothing changed.
	}

	revert := revert.New()
	defer revert.Fail()

	// Define a function which reverts everything.
	revert.Add(func() {
		// Reset changes to all nodes and database.
		n.common.update(oldNetwork, targetNode, clusterNotification)
	})

	// Apply changes to database.
	err = n.common.update(newNetwork, targetNode, clusterNotification)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}
