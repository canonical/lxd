package network

import (
	"fmt"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/validate"
)

// macvlan represents a LXD macvlan network.
type macvlan struct {
	common
}

// Type returns the network type.
func (n *macvlan) Type() string {
	return "macvlan"
}

// DBType returns the network type DB ID.
func (n *macvlan) DBType() db.NetworkType {
	return db.NetworkTypeMacvlan
}

// Validate network config.
func (n *macvlan) Validate(config map[string]string) error {
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
func (n *macvlan) Delete(clientType cluster.ClientType) error {
	n.logger.Debug("Delete", log.Ctx{"clientType": clientType})
	return n.common.delete(clientType)
}

// Rename renames a network.
func (n *macvlan) Rename(newName string) error {
	n.logger.Debug("Rename", log.Ctx{"newName": newName})

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

	if n.status == api.NetworkStatusPending {
		return fmt.Errorf("Cannot start pending network")
	}

	return nil
}

// Stop stops is a no-op.
func (n *macvlan) Stop() error {
	n.logger.Debug("Stop")

	return nil
}

// Update updates the network. Accepts notification boolean indicating if this update request is coming from a
// cluster notification, in which case do not update the database, just apply local changes needed.
func (n *macvlan) Update(newNetwork api.NetworkPut, targetNode string, clientType cluster.ClientType) error {
	n.logger.Debug("Update", log.Ctx{"clientType": clientType, "newNetwork": newNetwork})

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
		n.common.update(oldNetwork, targetNode, clientType)
	})

	// Apply changes to database.
	err = n.common.update(newNetwork, targetNode, clientType)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}
