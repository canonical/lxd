package network

import (
	"fmt"
	"net"

	"github.com/lxc/lxd/lxd/network/openvswitch"
)

// OVNInstanceDevicePortAdd adds a logical port to the OVN network's internal switch and returns the logical
// port name for use linking an OVS port on the integration bridge to the logical switch port.
func OVNInstanceDevicePortAdd(network Network, instanceID int, deviceName string, mac net.HardwareAddr, ips []net.IP) (openvswitch.OVNSwitchPort, error) {
	// Check network is of type OVN.
	n, ok := network.(*ovn)
	if !ok {
		return "", fmt.Errorf("Network is not OVN type")
	}

	return n.instanceDevicePortAdd(instanceID, deviceName, mac, ips)
}

// OVNInstanceDevicePortDelete deletes a logical port from the OVN network's internal switch.
func OVNInstanceDevicePortDelete(network Network, instanceID int, deviceName string) error {
	// Check network is of type OVN.
	n, ok := network.(*ovn)
	if !ok {
		return fmt.Errorf("Network is not OVN type")
	}

	return n.instanceDevicePortDelete(instanceID, deviceName)
}
