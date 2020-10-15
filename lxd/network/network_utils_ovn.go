package network

import (
	"fmt"
	"net"

	"github.com/lxc/lxd/lxd/network/openvswitch"
)

// OVNInstanceDevicePortAdd adds a logical port to the OVN network's internal switch and returns the logical
// port name for use linking an OVS port on the integration bridge to the logical switch port.
func OVNInstanceDevicePortAdd(network Network, instanceID int, instanceName string, deviceName string, mac net.HardwareAddr, ips []net.IP, internalRoutes []*net.IPNet, externalRoutes []*net.IPNet) (openvswitch.OVNSwitchPort, error) {
	// Check network is of type OVN.
	n, ok := network.(*ovn)
	if !ok {
		return "", fmt.Errorf("Network is not OVN type")
	}

	return n.instanceDevicePortAdd(instanceID, instanceName, deviceName, mac, ips, internalRoutes, externalRoutes)
}

// OVNInstanceDevicePortDynamicIPs gets a logical port's dynamic IPs stored in the OVN network's internal switch.
func OVNInstanceDevicePortDynamicIPs(network Network, instanceID int, deviceName string) ([]net.IP, error) {
	// Check network is of type OVN.
	n, ok := network.(*ovn)
	if !ok {
		return nil, fmt.Errorf("Network is not OVN type")
	}

	return n.instanceDevicePortDynamicIPs(instanceID, deviceName)
}

// OVNInstanceDevicePortDelete deletes a logical port from the OVN network's internal switch.
func OVNInstanceDevicePortDelete(network Network, instanceID int, deviceName string, internalRoutes []*net.IPNet, externalRoutes []*net.IPNet) error {
	// Check network is of type OVN.
	n, ok := network.(*ovn)
	if !ok {
		return fmt.Errorf("Network is not OVN type")
	}

	return n.instanceDevicePortDelete(instanceID, deviceName, internalRoutes, externalRoutes)
}
