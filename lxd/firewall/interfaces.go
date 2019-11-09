package firewall

import (
	"net"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
)

// proxy.go:
//   setupNAT() - set ipv4 and ipv6 prerouting and output nat
// nic_bridged.go:
//   removeFilters() - clear ipv6 filters and set ebtables to default
//     generateFilterEbtablesRules()
//     matchEbtablesRule()
//   setFilters() - set ebtables defaults and iptables defaults
//     generateFilterEbtablesRules()
//     generateFilterIptablesRules()
// networks.go
//   Setup()
//     - Configure IPv4 firewall for DHCP/DNS: 1246
//     - Allow IPv4 forwarding: 1271
//     - Configure IPv4 NAT: 1371
//     - Update IPv6 iptables DHCP/DNS overrides in the dnsmasq config: 1461
//     - Allow IPv6 forwarding: 1505
//     - Configure IPv6 NAT: 1565
//     - Configure tunnel (?) NAT: 1735

// Firewall represents an LXD firewall.
type Firewall interface {
	// Filter functions
	// FOLLOWS: functions which utilize iptables/ebtables
	// removeFilters() error // FIXME args (nic_bridged)
	// setFilters() error // FIXME args (nic_bridged)
	// Stop() error // (proxy)
	// setupNAT() error // (proxy)
	// Setup() error // (networks)
	// Stop() error // (networks)
	// needs <shared>, (m deviceConfig.Device) <deviceConfig>, (d *nicBridged)

	// NOTE: requires generateFilterEbtablesRules()
	// NOTE: requires matchEbtablesRule()
	// NOTE: xtables will need to include shared
	// NOTE: nicBridged may need generate/filter functions for nft

	// Lower-level functions
	NetworkClear(protocol string, comment string, table string) error
	ContainerClear(protocol string, comment string, table string) error
	VerifyIPv6Module() error

	// Proxy
	InstanceProxySetupNAT(protocol string, ipAddr net.IP, comment string, connType, address, port string, cPort string) error

	// NIC bridged
	InstanceNicBridgedRemoveFilters(m deviceConfig.Device, ipv4 net.IP, ipv6 net.IP) error
	InstanceNicBridgedSetFilters(m deviceConfig.Device, config map[string]string, ipv4 net.IP, ipv6 net.IP, name string) error

	// Network
	NetworkSetupConfigIPv4Firewall(name string, config map[string]string) error
	NetworkSetupAllowIPv4Forwarding(name string, config map[string]string) error
	NetworkSetupConfigIPv4NAT(name string, config map[string]string, subnet net.IPNet) error
	NetworkSetupConfigIPv6Firewall(name string) error
	NetworkSetupAllowIPv6Forwarding(name string, config map[string]string) error
	NetworkSetupConfigIPv6NAT(name string, config map[string]string, subnet net.IPNet) error
	NetworkSetupConfigTunnelNAT(name string, config map[string]string, overlaySubnet net.IPNet) error
}
