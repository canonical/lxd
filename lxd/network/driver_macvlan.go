package network

import (
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/validate"
)

// macvlan represents a LXD macvlan network.
type macvlan struct {
	common
}

// DBType returns the network type DB ID.
func (n *macvlan) DBType() db.NetworkType {
	return db.NetworkTypeMacvlan
}

// State returns the network state.
func (n *macvlan) State() (*api.NetworkState, error) {
	parentState, err := resources.GetNetworkState(GetHostDevice(n.config["parent"], n.config["vlan"]))
	if err != nil {
		// If the parent is not found, return a response indicating the network is unavailable.
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return &api.NetworkState{
				State: "unavailable",
				Type:  "unknown",
			}, nil
		}

		// In all other cases, return the original error.
		return nil, err
	}

	var mtu int

	configMTU := n.config["mtu"]
	if configMTU != "" {
		uintMTU, err := strconv.ParseUint(configMTU, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("Invalid MTU specified %q: %w", configMTU, err)
		}

		// Bound check the MTU value before converting to int.
		if uintMTU > math.MaxInt32 {
			mtu = math.MaxInt32
		} else if uintMTU > 0 {
			mtu = int(uintMTU)
		} else {
			mtu = 1500
		}
	} else {
		mtu = parentState.Mtu
	}

	return &api.NetworkState{
		Addresses: []api.NetworkStateAddress{},
		Counters:  api.NetworkStateCounters{},
		Hwaddr:    parentState.Hwaddr,
		Mtu:       mtu,
		State:     parentState.State,
		Type:      "broadcast",
	}, nil
}

// Validate network config.
func (n *macvlan) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		// lxdmeta:generate(entities=network-macvlan; group=network-conf; key=parent)
		//
		// ---
		//  type: string
		//  shortdesc: Parent interface to create `macvlan` NICs on
		//  scope: local
		"parent": validate.Required(validate.IsNotEmpty, validate.IsInterfaceName),
		// lxdmeta:generate(entities=network-macvlan; group=network-conf; key=mtu)
		//
		// ---
		//  type: integer
		//  shortdesc: MTU of the new interface
		//  scope: global
		"mtu": validate.Optional(validate.IsNetworkMTU),
		// lxdmeta:generate(entities=network-macvlan; group=network-conf; key=vlan)
		//
		// ---
		//  type: integer
		//  shortdesc: VLAN ID to attach to
		//  scope: global
		"vlan": validate.Optional(validate.IsNetworkVLAN),
		// lxdmeta:generate(entities=network-macvlan; group=network-conf; key=gvrp)
		// This option specifies whether to register the VLAN using the GARP VLAN Registration Protocol.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  shortdesc: Whether to use GARP VLAN Registration Protocol
		//  scope: global
		"gvrp": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=network-macvlan; group=network-conf; key=maas.subnet.ipv4)
		//
		// ---
		//  type: string
		//  condition: IPv4 address; using the `network` property on the NIC
		//  shortdesc: MAAS IPv4 subnet to register instances in
		//  scope: global
		"maas.subnet.ipv4": validate.IsAny,
		// lxdmeta:generate(entities=network-macvlan; group=network-conf; key=maas.subnet.ipv6)
		//
		// ---
		//  type: string
		//  condition: IPv4 address; using the `network` property on the NIC
		//  shortdesc: MAAS IPv6 subnet to register instances in
		//  scope: global
		"maas.subnet.ipv6": validate.IsAny,

		// lxdmeta:generate(entities=network-macvlan; group=network-conf; key=user.*)
		//
		// ---
		//  type: string
		//  shortdesc: User-provided free-form key/value pairs
		//  scope: global
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

	return n.delete()
}

// Rename renames a network.
func (n *macvlan) Rename(newName string) error {
	n.logger.Debug("Rename", logger.Ctx{"newName": newName})

	// Rename common steps.
	err := n.rename(newName)
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

	dbUpdateNeeded, _, oldNetwork, err := n.configChanged(newNetwork)
	if err != nil {
		return err
	}

	if !dbUpdateNeeded {
		return nil // Nothing changed.
	}

	// If the network as a whole has not had any previous creation attempts, or the node itself is still
	// pending, then don't apply the new settings to the node, just to the database record (ready for the
	// actual global create request to be initiated).
	if n.Status() == api.NetworkStatusPending || n.LocalStatus() == api.NetworkStatusPending {
		return n.update(newNetwork, targetNode, clientType)
	}

	revert := revert.New()
	defer revert.Fail()

	// Define a function which reverts everything.
	revert.Add(func() {
		// Reset changes to all nodes and database.
		_ = n.update(oldNetwork, targetNode, clientType)
	})

	// Apply changes to all nodes and databse.
	err = n.update(newNetwork, targetNode, clientType)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}
