package network

import (
	"github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/validate"
)

// sriov represents a LXD sriov network.
type sriov struct {
	common
}

// Type returns the network type.
func (n *sriov) Type() string {
	return "sriov"
}

// DBType returns the network type DB ID.
func (n *sriov) DBType() db.NetworkType {
	return db.NetworkTypeSriov
}

// Validate network config.
func (n *sriov) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		"parent":           validate.Required(validate.IsNotEmpty, validInterfaceName),
		"mtu":              validate.Optional(validate.IsNetworkMTU),
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
func (n *sriov) Delete(clientType request.ClientType) error {
	n.logger.Debug("Delete", log.Ctx{"clientType": clientType})

	return n.common.delete(clientType)
}

// Rename renames a network.
func (n *sriov) Rename(newName string) error {
	n.logger.Debug("Rename", log.Ctx{"newName": newName})

	// Rename common steps.
	err := n.common.rename(newName)
	if err != nil {
		return err
	}

	return nil
}

// Start starts is a no-op.
func (n *sriov) Start() error {
	n.logger.Debug("Start")

	return nil
}

// Stop stops is a no-op.
func (n *sriov) Stop() error {
	n.logger.Debug("Stop")

	return nil
}

// Update updates the network. Accepts notification boolean indicating if this update request is coming from a
// cluster notification, in which case do not update the database, just apply local changes needed.
func (n *sriov) Update(newNetwork api.NetworkPut, targetNode string, clientType request.ClientType) error {
	n.logger.Debug("Update", log.Ctx{"clientType": clientType, "newNetwork": newNetwork})

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
		n.common.update(oldNetwork, targetNode, clientType)
	})

	// Apply changes to all nodes and databse.
	err = n.common.update(newNetwork, targetNode, clientType)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}
