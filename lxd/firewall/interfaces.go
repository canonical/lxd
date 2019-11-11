package firewall

import (
	"net"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
)


// Firewall represents an LXD firewall.
type Firewall interface {
	// Lower-level Functions
	NetworkClear(protocol string, comment string, table string) error
	ContainerClear(protocol string, comment string, table string) error
	VerifyIPv6Module() error

	// Proxy Functions
	InstanceProxySetupNAT(protocol string, ipAddr net.IP, comment string, connType, address, port string, cPort string) error

	// NIC Bridged Functions
	InstanceNicBridgedRemoveFilters(m deviceConfig.Device, ipv4 net.IP, ipv6 net.IP) error
	InstanceNicBridgedSetFilters(m deviceConfig.Device, config map[string]string, ipv4 net.IP, ipv6 net.IP, name string) error

	// Network FunctionsFunctions
	NetworkSetupConfigIPv4Firewall(name string, config map[string]string) error
	NetworkSetupAllowIPv4Forwarding(name string, config map[string]string) error
	NetworkSetupConfigIPv4NAT(name string, config map[string]string, subnet net.IPNet) error
	NetworkSetupConfigIPv6Firewall(name string) error
	NetworkSetupAllowIPv6Forwarding(name string, config map[string]string) error
	NetworkSetupConfigIPv6NAT(name string, config map[string]string, subnet net.IPNet) error
	NetworkSetupConfigTunnelNAT(name string, config map[string]string, overlaySubnet net.IPNet) error
}
