package firewall

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
//   Stop()

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

	// Network
	NetworkAppend(protocol string, comment string, table string, chain string, rule ...string) error
	NetworkPrepend(protocol string, comment string, table string, chain string, rule ...string) error
	NetworkClear(protocol string, comment string, table string) error

	// Container
	ContainerPrepend(protocol string, comment string, table string, chain string, rule ...string) error
	ContainerClear(protocol string, comment string, table string, chain string, rule ...string) error
}
