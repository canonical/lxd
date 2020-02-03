package firewall

import (
	"net"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/firewall/drivers"
)

// Firewall represents an LXD firewall.
type Firewall interface {
	// Lower-level Functions
	NetworkClear(family drivers.Family, table drivers.Table, comment string) error
	InstanceClear(family drivers.Family, table drivers.Table, comment string) error
	VerifyIPv6Module() error

	// Proxy Functions
	InstanceProxySetupNAT(family drivers.Family, connType, address, port string, destAddr net.IP, destPort string, comment string) error

	// NIC Bridged Functions
	InstanceNicBridgedRemoveFilters(m deviceConfig.Device, ipv4 net.IP, ipv6 net.IP) error
	InstanceNicBridgedSetFilters(m deviceConfig.Device, ipv4 net.IP, ipv6 net.IP, comment string) error

	// Network Functions
	NetworkSetupAllowForwarding(family drivers.Family, name string, actionType drivers.Action) error
	NetworkSetupNAT(family drivers.Family, name string, location drivers.Location, args ...string) error
	NetworkSetupIPv4DNSOverrides(name string) error
	NetworkSetupIPv4DHCPWorkaround(name string) error
	NetworkSetupIPv6DNSOverrides(name string) error
	NetworkSetupTunnelNAT(name string, location drivers.Location, overlaySubnet net.IPNet) error
}
