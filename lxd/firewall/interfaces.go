package firewall

import (
	"net"

	"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
)


// Firewall represents an LXD firewall.
type Firewall interface {
	// Lower-level Functions
	NetworkClear(name string, protocol string, table string) error
	InstanceClear(inst device.Instance, protocol string, table string) error
	VerifyIPv6Module() error

	// Proxy Functions
	InstanceProxySetupNAT(protocol string, ipAddr net.IP, comment string, connType, address, port string, cPort string) error

	// NIC Bridged Functions
	InstanceNicBridgedRemoveFilters(m deviceConfig.Device, ipv4 net.IP, ipv6 net.IP) error
	InstanceNicBridgedSetFilters(m deviceConfig.Device, config map[string]string, ipv4 net.IP, ipv6 net.IP, name string) error

	// Network Functions
	NetworkSetupAllowForwarding(protocol string, name string, should_accept bool) error
	NetworkSetupNAT(protocol string, name string, is_after bool, args ...string) error
	NetworkSetupIPv4DNSOverrides(name string) error
	NetworkSetupIPv4DHCPWorkaround(name string) error
	NetworkSetupIPv6DNSOverrides(name string) error
	NetworkSetupTunnelNAT(name string, is_after bool, overlaySubnet net.IPNet) error
}
