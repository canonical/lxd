package ovs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"

	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"

	"github.com/canonical/lxd/lxd/ip"
	ovsSwitch "github.com/canonical/lxd/lxd/network/ovs/schema/ovs"
	"github.com/canonical/lxd/shared"
)

// ovnBridgeMappingMutex locks access to read/write external-ids:ovn-bridge-mappings.
var ovnBridgeMappingMutex sync.Mutex

// BridgeExists returns true if the bridge exists.
func (o *VSwitch) BridgeExists(bridgeName string) (bool, error) {
	ctx := context.TODO()
	bridge := &ovsSwitch.Bridge{Name: bridgeName}

	err := o.client.Get(ctx, bridge)
	if err != nil {
		if err == ovsdbClient.ErrNotFound {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// BridgeAdd adds a new bridge.
func (o *VSwitch) BridgeAdd(bridgeName string, mayExist bool, hwaddr net.HardwareAddr, mtu uint32) error {
	args := []string{}

	if mayExist {
		args = append(args, "--may-exist")
	}

	args = append(args, "add-br", bridgeName)

	if hwaddr != nil {
		args = append(args, "--", "set", "bridge", bridgeName, fmt.Sprintf(`other-config:hwaddr="%s"`, hwaddr.String()))
	}

	if mtu > 0 {
		args = append(args, "--", "set", "int", bridgeName, fmt.Sprintf(`mtu_request=%d`, mtu))
	}

	_, err := shared.RunCommandContext(context.TODO(), "ovs-vsctl", args...)
	if err != nil {
		return err
	}

	return nil
}

// BridgeDelete deletes a bridge.
func (o *VSwitch) BridgeDelete(bridgeName string) error {
	_, err := shared.RunCommandContext(context.TODO(), "ovs-vsctl", "del-br", bridgeName)
	if err != nil {
		return err
	}

	return nil
}

// BridgePortAdd adds a port to the bridge (if already attached does nothing).
func (o *VSwitch) BridgePortAdd(bridgeName string, portName string, mayExist bool) error {
	args := []string{}

	if mayExist {
		args = append(args, "--may-exist")
	}

	args = append(args, "add-port", bridgeName, portName)
	_, err := shared.RunCommandContext(context.TODO(), "ovs-vsctl", args...)
	if err != nil {
		return err
	}

	return nil
}

// BridgePortDelete deletes a port from the bridge (if already detached does nothing).
func (o *VSwitch) BridgePortDelete(bridgeName string, portName string) error {
	_, err := shared.RunCommandContext(context.TODO(), "ovs-vsctl", "--if-exists", "del-port", bridgeName, portName)
	if err != nil {
		return err
	}

	return nil
}

// BridgePortSet sets port options.
func (o *VSwitch) BridgePortSet(portName string, options ...string) error {
	_, err := shared.RunCommandContext(context.TODO(), "ovs-vsctl", append([]string{"set", "port", portName}, options...)...)
	if err != nil {
		return err
	}

	return nil
}

// InterfaceAssociateOVNSwitchPort removes any existing switch ports associated to the specified ovnSwitchPortName
// and then associates the specified interfaceName to the OVN switch port.
func (o *VSwitch) InterfaceAssociateOVNSwitchPort(interfaceName string, ovnSwitchPortName string) error {
	// Clear existing ports that were formerly associated to ovnSwitchPortName.
	existingPorts, err := shared.RunCommandContext(context.TODO(), "ovs-vsctl", "--format=csv", "--no-headings", "--data=bare", "--columns=name", "find", "interface", "external-ids:iface-id="+string(ovnSwitchPortName))
	if err != nil {
		return err
	}

	existingPorts = strings.TrimSpace(existingPorts)
	if existingPorts != "" {
		for port := range strings.SplitSeq(existingPorts, "\n") {
			_, err = shared.RunCommandContext(context.TODO(), "ovs-vsctl", "del-port", port)
			if err != nil {
				return err
			}

			// Attempt to remove port, but don't fail if doesn't exist or can't be removed, at least
			// the switch association has been successfully removed, so the new port being added next
			// won't fail to work properly.
			link := &ip.Link{Name: port}
			_ = link.Delete()
		}
	}

	_, err = shared.RunCommandContext(context.TODO(), "ovs-vsctl", "set", "interface", interfaceName, "external_ids:iface-id="+string(ovnSwitchPortName))
	if err != nil {
		return err
	}

	return nil
}

// ChassisID returns the local chassis ID.
func (o *VSwitch) ChassisID() (string, error) {
	ctx := context.TODO()

	vSwitch := &ovsSwitch.OpenvSwitch{
		UUID: o.rootUUID,
	}

	err := o.client.Get(ctx, vSwitch)
	if err != nil {
		return "", err
	}

	val := vSwitch.ExternalIDs["system-id"]
	return val, nil
}

// OVNEncapIP returns the enscapsulation IP used for OVN underlay tunnels.
func (o *VSwitch) OVNEncapIP() (net.IP, error) {
	// ovs-vsctl's get command doesn't support its --format flag, so we always get the output quoted.
	// However ovs-vsctl's find and list commands don't support retrieving a single column's map field.
	// And ovs-vsctl's JSON output is unfriendly towards statically typed languages as it mixes data types
	// in a slice. So stick with "get" command and use Go's strconv.Unquote to return the actual values.
	encapIPStr, err := shared.RunCommandContext(context.TODO(), "ovs-vsctl", "get", "open_vswitch", ".", "external_ids:ovn-encap-ip")
	if err != nil {
		return nil, err
	}

	encapIPStr = strings.TrimSpace(encapIPStr)
	encapIPStr, err = unquote(encapIPStr)
	if err != nil {
		return nil, fmt.Errorf("Failed unquoting: %w", err)
	}

	encapIP := net.ParseIP(encapIPStr)
	if encapIP == nil {
		return nil, errors.New("Invalid ovn-encap-ip address")
	}

	return encapIP, nil
}

// OVNBridgeMappings gets the current OVN bridge mappings.
func (o *VSwitch) OVNBridgeMappings(bridgeName string) ([]string, error) {
	ctx := context.TODO()

	// Get the root switch.
	vSwitch := &ovsSwitch.OpenvSwitch{
		UUID: o.rootUUID,
	}

	err := o.client.Get(ctx, vSwitch)
	if err != nil {
		return nil, err
	}

	// Return the bridge mappings.
	val := vSwitch.ExternalIDs["ovn-bridge-mappings"]
	if val == "" {
		return []string{}, nil
	}

	return strings.Split(val, ","), nil
}

// OVNBridgeMappingAdd appends an OVN bridge mapping between a bridge and the logical provider name.
func (o *VSwitch) OVNBridgeMappingAdd(bridgeName string, providerName string) error {
	ovnBridgeMappingMutex.Lock()
	defer ovnBridgeMappingMutex.Unlock()

	mappings, err := o.OVNBridgeMappings(bridgeName)
	if err != nil {
		return err
	}

	newMapping := fmt.Sprintf("%s:%s", providerName, bridgeName)
	if slices.Contains(mappings, newMapping) {
		return nil // Mapping is already present, nothing to do.
	}

	mappings = append(mappings, newMapping)

	// Set new mapping string back into the database.
	_, err = shared.RunCommandContext(context.TODO(), "ovs-vsctl", "set", "open_vswitch", ".", "external-ids:ovn-bridge-mappings="+strings.Join(mappings, ","))
	if err != nil {
		return err
	}

	return nil
}

// OVNBridgeMappingDelete deletes an OVN bridge mapping between a bridge and the logical provider name.
func (o *VSwitch) OVNBridgeMappingDelete(bridgeName string, providerName string) error {
	ovnBridgeMappingMutex.Lock()
	defer ovnBridgeMappingMutex.Unlock()

	mappings, err := o.OVNBridgeMappings(bridgeName)
	if err != nil {
		return err
	}

	changed := false
	newMappings := make([]string, 0, len(mappings))
	matchMapping := fmt.Sprintf("%s:%s", providerName, bridgeName)
	for _, mapping := range mappings {
		if mapping != matchMapping {
			newMappings = append(newMappings, mapping)
		} else {
			changed = true
		}
	}

	if changed {
		if len(newMappings) < 1 {
			// Remove mapping key in the database.
			_, err = shared.RunCommandContext(context.TODO(), "ovs-vsctl", "remove", "open_vswitch", ".", "external-ids", "ovn-bridge-mappings")
			if err != nil {
				return err
			}
		} else {
			// Set updated mapping string back into the database.
			_, err = shared.RunCommandContext(context.TODO(), "ovs-vsctl", "set", "open_vswitch", ".", "external-ids:ovn-bridge-mappings="+strings.Join(newMappings, ","))
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// BridgePortList returns a list of ports that are connected to the bridge.
func (o *VSwitch) BridgePortList(bridgeName string) ([]string, error) {
	// Clear existing ports that were formerly associated to ovnSwitchPortName.
	portString, err := shared.RunCommandContext(context.TODO(), "ovs-vsctl", "list-ports", bridgeName)
	if err != nil {
		return nil, err
	}

	ports := []string{}

	portString = strings.TrimSpace(portString)
	if portString != "" {
		for port := range strings.SplitSeq(portString, "\n") {
			ports = append(ports, strings.TrimSpace(port))
		}
	}

	return ports, nil
}

// HardwareOffloadingEnabled returns true if hardware offloading is enabled.
func (o *VSwitch) HardwareOffloadingEnabled() bool {
	// ovs-vsctl's get command doesn't support its --format flag, so we always get the output quoted.
	// However ovs-vsctl's find and list commands don't support retrieving a single column's map field.
	// And ovs-vsctl's JSON output is unfriendly towards statically typed languages as it mixes data types
	// in a slice. So stick with "get" command and use Go's strconv.Unquote to return the actual values.
	offload, err := shared.RunCommandContext(context.TODO(), "ovs-vsctl", "--if-exists", "get", "open_vswitch", ".", "other_config:hw-offload")
	if err != nil {
		return false
	}

	offload = strings.TrimSpace(offload)
	if offload == "" {
		return false
	}

	offload, err = unquote(offload)
	if err != nil {
		return false
	}

	return offload == "true"
}

// OVNSouthboundDBRemoteAddress gets the address of the southbound ovn database.
func (o *VSwitch) OVNSouthboundDBRemoteAddress() (string, error) {
	result, err := shared.RunCommandContext(context.TODO(), "ovs-vsctl", "get", "open_vswitch", ".", "external_ids:ovn-remote")
	if err != nil {
		return "", err
	}

	addr, err := unquote(strings.TrimSuffix(result, "\n"))
	if err != nil {
		return "", err
	}

	return addr, nil
}

// getSTPPriority returns the STP priority of the OVS bridge.
// Default STP priority is 32768 (0x8000). This value will be used if the priority cannot be retrieved or if STP is disabled.
// The STP range in OVS is 0 to 65,535: http://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.txt
func getSTPPriority(ctx context.Context, bridgeName string) (uint16, error) {
	const defaultSTPPriority = 32768

	output, err := shared.RunCommandContext(ctx, "ovs-vsctl", "get", "bridge", bridgeName, "other_config:stp-priority")
	// get bridge does not differentiate in it's error codes between variable undefined and other errors, such as bridge not found
	// the error output will need to be checked to make sure if stp-priority is not defined (defaults to 0x8000) or if a breaking error happened
	if err != nil {
		// default stp-priority is used if stp-priority is not defined
		if strings.Contains(err.Error(), fmt.Sprintf(`no key "stp-priority" in Bridge record %q column other_config`, bridgeName)) {
			return defaultSTPPriority, nil
		}

		// All other errors are considered errors
		return defaultSTPPriority, err
	}

	// Convert to hex format
	output = strings.TrimSpace(output)
	// remove surrounding quotes
	output = output[1 : len(output)-1]
	stpPriority, err := strconv.ParseUint(output, 10, 16)
	if err != nil {
		return defaultSTPPriority, err
	}

	return uint16(stpPriority), nil
}

// GenerateOVSBridgeID returns the bridge ID of the bridge.
// The bridge IDs follow the following format <STP priority>.<MAC Address>.
// Check the Bridge ID section on https://www.kernel.org/doc/Documentation/networking/bridge.rst.
// The STP priority is in hexadecimal format just like the MAC address.
func (o *VSwitch) GenerateOVSBridgeID(ctx context.Context, bridgeName string) (string, error) {
	// get the MAC address
	netIf, err := net.InterfaceByName(bridgeName)
	if err != nil {
		return "", err
	}

	bridgeHwID := strings.ReplaceAll(strings.ToLower(netIf.HardwareAddr.String()), ":", "")

	// Get the STP priority
	stpPriority, err := getSTPPriority(ctx, bridgeName)
	if err != nil {
		return "", err
	}

	stpPriorityHex := fmt.Sprintf("%4X", stpPriority)
	return stpPriorityHex + "." + bridgeHwID, nil
}

// STPEnabled checks if STP is enabled by looking up the "stp_enable" boolean config variable.
// Returns the value stored in "stp_enable", or false if it is undefined.
func (o *VSwitch) STPEnabled(ctx context.Context, bridgeName string) (bool, error) {
	output, err := shared.RunCommandContext(ctx, "ovs-vsctl", "get", "bridge", bridgeName, "stp_enable")
	if err != nil {
		return false, err
	}

	output = strings.TrimSpace(output)
	return strconv.ParseBool(output)
}

// GetSTPForwardDelay returns the STP forward delay in ms. OVS returns the value in seconds, so it needs to be
// converted to ms to satisfy the api.NetworkStateBridge struct which expects the value in ms.
// Default value is 15s.
// Check the "other_config : stp-forward-delay:" section on http://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.txt
func (o *VSwitch) GetSTPForwardDelay(ctx context.Context, bridgeName string) (uint64, error) {
	const defaultSTPFwdDelay = 15000

	output, err := shared.RunCommandContext(ctx, "ovs-vsctl", "get", "bridge", bridgeName, "other_config:stp-forward-delay")
	// get bridge does not differentiate in it's error codes between variables undefined and other errors, such as bridge not found
	// the error output will need to be checked to make sure if stp-forward-delay is not defined (defaults to 15000) or if a breaking error happened
	if err != nil {
		// default stp-priority is used if stp-priority is not defined
		if strings.Contains(err.Error(), fmt.Sprintf(`no key "stp-forward-delay" in Bridge record %q column other_config`, bridgeName)) {
			return defaultSTPFwdDelay, nil
		}

		// All other errors are considered errors
		return defaultSTPFwdDelay, err
	}

	output = strings.TrimSpace(output)
	// remove surrounding quotes
	output = output[1 : len(output)-1]
	// Convert to uint64
	stpFwdDelay, err := strconv.ParseUint(output, 10, 64)
	if err != nil {
		return defaultSTPFwdDelay, err
	}

	return stpFwdDelay * 1000, nil
}

// VLANFilteringEnabled checks if a vlans are enabled on the bridge.
// In OVS, Vlan filtering is enabled when Vlan related settings are configured.
func (o *VSwitch) VLANFilteringEnabled(ctx context.Context, bridgeName string) (bool, error) {
	// check if the tag, trunks or vlan_mode fields are populated
	output, err := shared.RunCommandContext(ctx, "ovs-vsctl", "get", "port", bridgeName, "tag", "trunks", "vlan_mode")
	if err != nil {
		return false, err
	}

	lines := strings.SplitSeq(strings.TrimSpace(output), "\n")
	for line := range lines {
		// when no value is defined "[]" is returned
		if line != "[]" {
			return true, nil
		}
	}

	return false, nil
}

// GetVLANPVID returns the PVID of the bridge.
// In OVS a PVID of 0 means that the port is not associated with any VLAN.
func (o *VSwitch) GetVLANPVID(ctx context.Context, bridgeName string) (uint64, error) {
	output, err := shared.RunCommandContext(ctx, "ovs-vsctl", "get", "port", bridgeName, "tag")
	if err != nil {
		return 0, err
	}

	output = strings.TrimSpace(output)
	if output == "[]" {
		return 0, nil
	}

	pvid, err := strconv.ParseUint(output, 10, 64)
	if err != nil {
		return 0, err
	}

	return pvid, nil
}
