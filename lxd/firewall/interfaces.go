package firewall

import (
	"net"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	firewallConsts "github.com/lxc/lxd/lxd/firewall/consts"
	"github.com/lxc/lxd/lxd/iptables"
)

// Firewall represents an LXD firewall.
type Firewall interface {
	// Lower-level Functions
	NetworkClear(family firewallConsts.Family, table firewallConsts.Table, comment string) error
	InstanceClear(family firewallConsts.Family, table firewallConsts.Table, comment string) error
	VerifyIPv6Module() error

	// Proxy Functions
	InstanceProxySetupNAT(family firewallConsts.Family, connType, address, port string, destAddr net.IP, destPort string, comment string) error

	// NIC Bridged Functions
	InstanceNicBridgedRemoveFilters(m deviceConfig.Device, ipv4 net.IP, ipv6 net.IP) error
	InstanceNicBridgedSetFilters(m deviceConfig.Device, ipv4 net.IP, ipv6 net.IP, comment string) error

	// Network Functions
	NetworkSetupAllowForwarding(family firewallConsts.Family, name string, actionType firewallConsts.Action) error
	NetworkSetupNAT(family firewallConsts.Family, name string, location firewallConsts.Location, args ...string) error
	NetworkSetupIPv4DNSOverrides(name string) error
	NetworkSetupIPv4DHCPWorkaround(name string) error
	NetworkSetupIPv6DNSOverrides(name string) error
	NetworkSetupTunnelNAT(name string, location firewallConsts.Location, overlaySubnet net.IPNet) error
}

// New returns an appropriate firewall implementation.
func New() Firewall {
	// TODO: Issue #6223: add startup logic to choose xtables or nftables
	return iptables.XTables{}
}
