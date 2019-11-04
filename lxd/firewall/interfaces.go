package firewall

import (
	"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
)

// proxy.go:
//   Stop() - clear both ipv4 and ipv6 instance nat
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
//     - Remove existing IPv4 iptables rules: 1208
//     - Clear iptables NAT config: 1221
//     - Configure IPv4 firewall for DHCP/DNS: 1246
//     - Allow IPv4 forwarding: 1271
//     - Configure IPv4 NAT: 1371
//     - Remove existing IPv6 iptables rules: 1411
//     - Update IPv6 iptables DHCP/DNS overrides in the dnsmasq config: 1461
//     - Allow IPv6 forwarding: 1505
//     - Configure IPv6 NAT: 1565
//     - Configure tunnel (?) NAT: 1735
//   Stop()
//     - Cleanup IPv4 iptables: 1985
//     - Cleanup IPv6 iptables: 2005

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

	// Proxy
	ProxyStop() (*device.RunConfig, error)
	ProxySetupNAT()

	// NIC bridged
	BridgeRemoveFilters(deviceConfig.Device) error
	BridgeSetFilters(deviceConfig.Device) error

	// Network
	NetworkSetup(map[string]string) error
	NetworkStop() error
}
