package drivers

import "net"

// FilterIPv6All used to indicate to firewall package to filter all IPv6 traffic.
const FilterIPv6All = "::"

// FilterIPv4All used to indicate to firewall package to filter all IPv4 traffic.
const FilterIPv4All = "0.0.0.0"

// FeatureOpts specify how firewall features are setup.
type FeatureOpts struct {
	ICMPDHCPDNSAccess bool // Add rules to allow ICMP, DHCP and DNS access.
	ForwardingAllow   bool // Add rules to allow IP forwarding. Blocked if false.
}

// SNATOpts specify how SNAT rules are setup.
type SNATOpts struct {
	Append      bool       // Append rules (has no effect if driver doesn't support it).
	Subnet      *net.IPNet // Subnet of source network used to identify candidate traffic.
	SNATAddress net.IP     // SNAT IP address to use. If nil then MASQUERADE is used.
}

// Opts for setting up the firewall.
type Opts struct {
	FeaturesV4 *FeatureOpts // Enable IPv4 firewall with specified options. Off if not provided.
	FeaturesV6 *FeatureOpts // Enable IPv6 firewall with specified options. Off if not provided.
	SNATV4     *SNATOpts    // Enable IPv4 SNAT with specified options. Off if not provided.
	SNATV6     *SNATOpts    // Enable IPv6 SNAT with specified options. Off if not provided.
}

// AddressForward represents a NAT address forward.
type AddressForward struct {
	ListenAddress net.IP
	TargetAddress net.IP
	Protocol      string
	ListenPorts   []uint64
	TargetPorts   []uint64
}
