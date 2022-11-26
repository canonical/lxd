package network

import (
	"fmt"

	"github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/validate"
)

// macvlan represents a LXD macvlan network.
type macvlan struct {
	common
}

// DBType returns the network type DB ID.
func (n *macvlan) DBType() db.NetworkType {
	return db.NetworkTypeMacvlan
}

// Validate network config.
func (n *macvlan) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		"parent":           validate.Required(validate.IsNotEmpty, validate.IsInterfaceName),
		"mtu":              validate.Optional(validate.IsNetworkMTU),
		"vlan":             validate.Optional(validate.IsNetworkVLAN),
		"gvrp":             validate.Optional(validate.IsBool),
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
func (n *macvlan) Delete(clientType request.ClientType) error {
	n.logger.Debug("Delete", logger.Ctx{"clientType": clientType})

	return n.common.delete(clientType)
}

// Rename renames a network.
func (n *macvlan) Rename(newName string) error {
	n.logger.Debug("Rename", logger.Ctx{"newName": newName})

	// Rename common steps.
	err := n.common.rename(newName)
	if err != nil {
		return err
	}

	return nil
}

// Start starts is a no-op.
func (n *macvlan) Start() error {
	n.logger.Debug("Start")

	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() { n.setUnavailable() })

	if !InterfaceExists(n.config["parent"]) {
		return fmt.Errorf("Parent interface %q not found", n.config["parent"])
	}

	revert.Success()

	// Ensure network is marked as available now its started.
	n.setAvailable()

	return nil
}

// Stop stops is a no-op.
func (n *macvlan) Stop() error {
	n.logger.Debug("Stop")

	return nil
}

// Update updates the network. Accepts notification boolean indicating if this update request is coming from a
// cluster notification, in which case do not update the database, just apply local changes needed.
func (n *macvlan) Update(newNetwork api.NetworkPut, targetNode string, clientType request.ClientType) error {
	n.logger.Debug("Update", logger.Ctx{"clientType": clientType, "newNetwork": newNetwork})

	dbUpdateNeeeded, _, oldNetwork, err := n.common.configChanged(newNetwork)
	if err != nil {
		return err
	}

	if !dbUpdateNeeeded {
		return nil // Nothing changed.
	}

	// If the network as a whole has not had any previous creation attempts, or the node itself is still
	// pending, then don't apply the new settings to the node, just to the database record (ready for the
	// actual global create request to be initiated).
	if n.Status() == api.NetworkStatusPending || n.LocalStatus() == api.NetworkStatusPending {
		return n.common.update(newNetwork, targetNode, clientType)
	}

	revert := revert.New()
	defer revert.Fail()

	// Define a function which reverts everything.
	revert.Add(func() {
		// Reset changes to all nodes and database.
		_ = n.common.update(oldNetwork, targetNode, clientType)
	})

	// Apply changes to all nodes and databse.
	err = n.common.update(newNetwork, targetNode, clientType)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}
