package openvswitch

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/grant-he/lxd/shared"
)

// ovnBridgeMappingMutex locks access to read/write external-ids:ovn-bridge-mappings.
var ovnBridgeMappingMutex sync.Mutex

// NewOVS initialises new OVS wrapper.
func NewOVS() *OVS {
	return &OVS{}
}

// OVS command wrapper.
type OVS struct{}

// Installed returns true if OVS tools are installed.
func (o *OVS) Installed() bool {
	_, err := exec.LookPath("ovs-vsctl")
	if err != nil {
		return false
	}

	return true
}

// BridgeExists returns true if OVS bridge exists.
func (o *OVS) BridgeExists(bridgeName string) (bool, error) {
	_, err := shared.RunCommand("ovs-vsctl", "br-exists", bridgeName)
	if err != nil {
		runErr, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runErr.Err.(*exec.ExitError)

			// ovs-vsctl manpage says that br-exists exits with code 2 if bridge doesn't exist.
			if ok && exitError.ExitCode() == 2 {
				return false, nil
			}
		}

		return false, err
	}

	return true, nil
}

// BridgeAdd adds an OVS bridge.
func (o *OVS) BridgeAdd(bridgeName string, mayExist bool) error {
	args := []string{}

	if mayExist {
		args = append(args, "--may-exist")
	}

	args = append(args, "add-br", bridgeName)

	_, err := shared.RunCommand("ovs-vsctl", args...)
	if err != nil {
		return err
	}

	return nil
}

// BridgeDelete deletes an OVS bridge.
func (o *OVS) BridgeDelete(bridgeName string) error {
	_, err := shared.RunCommand("ovs-vsctl", "del-br", bridgeName)
	if err != nil {
		return err
	}

	return nil
}

// BridgePortAdd adds a port to the bridge (if already attached does nothing).
func (o *OVS) BridgePortAdd(bridgeName string, portName string, mayExist bool) error {
	args := []string{}

	if mayExist {
		args = append(args, "--may-exist")
	}

	args = append(args, "add-port", bridgeName, portName)

	_, err := shared.RunCommand("ovs-vsctl", args...)
	if err != nil {
		return err
	}

	return nil
}

// BridgePortDelete deletes a port from the bridge (if already detached does nothing).
func (o *OVS) BridgePortDelete(bridgeName string, portName string) error {
	_, err := shared.RunCommand("ovs-vsctl", "--if-exists", "del-port", bridgeName, portName)
	if err != nil {
		return err
	}

	return nil
}

// BridgePortSet sets port options.
func (o *OVS) BridgePortSet(portName string, options ...string) error {
	_, err := shared.RunCommand("ovs-vsctl", append([]string{"set", "port", portName}, options...)...)
	if err != nil {
		return err
	}

	return nil
}

// InterfaceAssociateOVNSwitchPort removes any existing OVS ports associated to the specified ovnSwitchPortName
// and then associates the specified interfaceName to the OVN switch port.
func (o *OVS) InterfaceAssociateOVNSwitchPort(interfaceName string, ovnSwitchPortName OVNSwitchPort) error {
	// Clear existing ports that were formerly associated to ovnSwitchPortName.
	existingPorts, err := shared.RunCommand("ovs-vsctl", "--format=csv", "--no-headings", "--data=bare", "--colum=name", "find", "interface", fmt.Sprintf("external-ids:iface-id=%s", string(ovnSwitchPortName)))
	if err != nil {
		return err
	}

	existingPorts = strings.TrimSpace(existingPorts)
	if existingPorts != "" {
		for _, port := range strings.Split(existingPorts, "\n") {
			_, err = shared.RunCommand("ovs-vsctl", "del-port", port)
			if err != nil {
				return err
			}

			// Atempt to remove port, but don't fail if doesn't exist or can't be removed, at least
			// the OVS association has been successfully removed, so the new port being added next
			// won't fail to work properly.
			shared.RunCommand("ip", "link", "del", port)
		}
	}

	_, err = shared.RunCommand("ovs-vsctl", "set", "interface", interfaceName, fmt.Sprintf("external_ids:iface-id=%s", string(ovnSwitchPortName)))
	if err != nil {
		return err
	}

	return nil
}

// ChassisID returns the local chassis ID.
func (o *OVS) ChassisID() (string, error) {
	// ovs-vsctl's get command doesn't support its --format flag, so we always get the output quoted.
	// However ovs-vsctl's find and list commands don't support retrieving a single column's map field.
	// And ovs-vsctl's JSON output is unfriendly towards statically typed languages as it mixes data types
	// in a slice. So stick with "get" command and use Go's strconv.Unquote to return the actual values.
	chassisID, err := shared.RunCommand("ovs-vsctl", "get", "open_vswitch", ".", "external_ids:system-id")
	if err != nil {
		return "", err
	}

	chassisID = strings.TrimSpace(chassisID)
	chassisID, err = strconv.Unquote(chassisID)
	if err != nil {
		return "", err
	}

	return chassisID, nil
}

// OVNEncapIP returns the enscapsulation IP used for OVN underlay tunnels.
func (o *OVS) OVNEncapIP() (net.IP, error) {
	// ovs-vsctl's get command doesn't support its --format flag, so we always get the output quoted.
	// However ovs-vsctl's find and list commands don't support retrieving a single column's map field.
	// And ovs-vsctl's JSON output is unfriendly towards statically typed languages as it mixes data types
	// in a slice. So stick with "get" command and use Go's strconv.Unquote to return the actual values.
	encapIPStr, err := shared.RunCommand("ovs-vsctl", "get", "open_vswitch", ".", "external_ids:ovn-encap-ip")
	if err != nil {
		return nil, err
	}

	encapIPStr = strings.TrimSpace(encapIPStr)
	encapIPStr, err = strconv.Unquote(encapIPStr)
	if err != nil {
		return nil, err
	}

	encapIP := net.ParseIP(encapIPStr)
	if encapIP == nil {
		return nil, fmt.Errorf("Invalid ovn-encap-ip address")
	}

	return encapIP, nil
}

// OVNBridgeMappings gets the current OVN bridge mappings.
func (o *OVS) OVNBridgeMappings(bridgeName string) ([]string, error) {
	// ovs-vsctl's get command doesn't support its --format flag, so we always get the output quoted.
	// However ovs-vsctl's find and list commands don't support retrieving a single column's map field.
	// And ovs-vsctl's JSON output is unfriendly towards statically typed languages as it mixes data types
	// in a slice. So stick with "get" command and use Go's strconv.Unquote to return the actual values.
	mappings, err := shared.RunCommand("ovs-vsctl", "--if-exists", "get", "open_vswitch", ".", "external-ids:ovn-bridge-mappings")
	if err != nil {
		return nil, err
	}

	mappings = strings.TrimSpace(mappings)
	if mappings == "" {
		return []string{}, nil
	}

	mappings, err = strconv.Unquote(mappings)
	if err != nil {
		return nil, err
	}

	return strings.SplitN(mappings, ",", -1), nil
}

// OVNBridgeMappingAdd appends an OVN bridge mapping between an OVS bridge and the logical provider name.
func (o *OVS) OVNBridgeMappingAdd(bridgeName string, providerName string) error {
	ovnBridgeMappingMutex.Lock()
	defer ovnBridgeMappingMutex.Unlock()

	mappings, err := o.OVNBridgeMappings(bridgeName)
	if err != nil {
		return err
	}

	newMapping := fmt.Sprintf("%s:%s", providerName, bridgeName)
	for _, mapping := range mappings {
		if mapping == newMapping {
			return nil // Mapping is already present, nothing to do.
		}
	}

	mappings = append(mappings, newMapping)

	// Set new mapping string back into OVS database.
	_, err = shared.RunCommand("ovs-vsctl", "set", "open_vswitch", ".", fmt.Sprintf("external-ids:ovn-bridge-mappings=%s", strings.Join(mappings, ",")))
	if err != nil {
		return err
	}

	return nil
}

// OVNBridgeMappingDelete deletes an OVN bridge mapping between an OVS bridge and the logical provider name.
func (o *OVS) OVNBridgeMappingDelete(bridgeName string, providerName string) error {
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
			// Remove mapping key in OVS database.
			_, err = shared.RunCommand("ovs-vsctl", "remove", "open_vswitch", ".", "external-ids", "ovn-bridge-mappings")
			if err != nil {
				return err
			}
		} else {
			// Set updated mapping string back into OVS database.
			_, err = shared.RunCommand("ovs-vsctl", "set", "open_vswitch", ".", fmt.Sprintf("external-ids:ovn-bridge-mappings=%s", strings.Join(newMappings, ",")))
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// BridgePortList returns a list of ports that are connected to the bridge.
func (o *OVS) BridgePortList(bridgeName string) ([]string, error) {
	// Clear existing ports that were formerly associated to ovnSwitchPortName.
	portString, err := shared.RunCommand("ovs-vsctl", "list-ports", bridgeName)
	if err != nil {
		return nil, err
	}

	ports := []string{}

	portString = strings.TrimSpace(portString)
	if portString != "" {
		for _, port := range strings.Split(portString, "\n") {
			ports = append(ports, strings.TrimSpace(port))
		}
	}

	return ports, nil
}
