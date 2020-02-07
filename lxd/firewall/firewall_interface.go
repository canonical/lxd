package firewall

import (
	"net"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
)

// Firewall represents an LXD firewall.
type Firewall interface {
	NetworkSetupForwardingPolicy(networkName string, ipVersion uint, allow bool) error
	NetworkSetupOutboundNAT(networkName string, subnet *net.IPNet, srcIP net.IP, append bool) error
	NetworkSetupDHCPDNSAccess(networkName string, ipVersion uint) error
	NetworkSetupDHCPv4Checksum(networkName string) error
	NetworkClear(networkName string, ipVersion uint) error

	InstanceSetupBridgeFilter(projectName, instanceName, deviceName, parentName, hostName, hwAddr string, IPv4, IPv6 net.IP) error
	InstanceClearBridgeFilter(projectName, instanceName, deviceName, parentName, hostName, hwAddr string, IPv4, IPv6 net.IP) error

	InstanceSetupProxyNAT(projectName, instanceName, deviceName string, listen, connect *deviceConfig.ProxyAddress) error
	InstanceClearProxyNAT(projectName, instanceName, deviceName string) error
}
