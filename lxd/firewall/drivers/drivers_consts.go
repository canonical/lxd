package drivers

import "net"

// FilterIPv6All used to indicate to firewall package to filter all IPv6 traffic.
const FilterIPv6All = "::"

// FilterIPv4All used to indicate to firewall package to filter all IPv4 traffic.
const FilterIPv4All = "0.0.0.0"

// FeatureOpts specify how firewall features are setup.
type FeatureOpts struct {
	DHCPDNSAccess   bool // Add rules to allow DHCP and DNS access.
	ForwardingAllow bool // Add rules to allow IP forwarding. Blocked if false.
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
	ACL        bool         // Enable ACL during setup.
}

// ACLRule represents an ACL rule that can be added to a firewall.
type ACLRule struct {
	Direction       string // Either "ingress" or "egress.
	Action          string
	Log             bool   // Whether or not to log matched packets.
	LogName         string // Log label name (requires Log be true).
	Source          string
	Destination     string
	Protocol        string
	SourcePort      string
	DestinationPort string
	ICMPType        string
	ICMPCode        string
}
