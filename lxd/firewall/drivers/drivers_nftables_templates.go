package drivers

import (
	"text/template"
)

var nftablesCommonTable = template.Must(template.New("nftablesCommonTable").Parse(`
table {{.family}} {{.namespace}} {
	{{- template "nftablesContent" . -}}
}
`))

var nftablesNetForwardingPolicy = template.Must(template.New("nftablesNetForwardingPolicy").Parse(`
chain fwd{{.chainSeparator}}{{.networkName}} {
	type filter hook forward priority 0; policy accept;
	oifname "{{.networkName}}" {{.action}}
	iifname "{{.networkName}}" {{.action}}
}
`))

var nftablesNetOutboundNAT = template.Must(template.New("nftablesNetOutboundNAT").Parse(`
chain pstrt{{.chainSeparator}}{{.networkName}} {
	type nat hook postrouting priority 100; policy accept;
	{{if .srcIP -}}
	{{.family}} saddr {{.subnet}} {{.family}} daddr != {{.subnet}} snat {{.srcIP}}
	{{else -}}
	{{.family}} saddr {{.subnet}} {{.family}} daddr != {{.subnet}} masquerade
	{{- end}}
}
`))

var nftablesNetDHCPDNS = template.Must(template.New("nftablesNetDHCPDNS").Parse(`
chain in{{.chainSeparator}}{{.networkName}} {
	type filter hook input priority 0; policy accept;
	iifname "{{.networkName}}" tcp dport 53 accept
	iifname "{{.networkName}}" udp dport 53 accept
	{{if eq .family "ip" -}}
	iifname "{{.networkName}}" udp dport 67 accept
	{{else -}}
	iifname "{{.networkName}}" udp dport 547 accept
	{{- end}}
}

chain out{{.chainSeparator}}{{.networkName}} {
	type filter hook output priority 0; policy accept;
	oifname "{{.networkName}}" tcp sport 53 accept
	oifname "{{.networkName}}" udp sport 53 accept
	{{if eq .family "ip" -}}
	oifname "{{.networkName}}" udp sport 67 accept
	{{else -}}
	oifname "{{.networkName}}" udp sport 547 accept
	{{- end}}
}
`))

var nftablesNetProxyNAT = template.Must(template.New("nftablesNetProxyNAT").Parse(`
chain prert{{.chainSeparator}}{{.deviceLabel}} {
	type nat hook prerouting priority -100; policy accept;
	{{- range .rules}}
	{{.family}} daddr {{.listenHost}} {{.connType}} dport {{.listenPort}} dnat to {{.connectDest}}
	{{- end}}
}

chain out{{.chainSeparator}}{{.deviceLabel}} {
	type nat hook output priority -100; policy accept;
	{{- range .rules}}
	{{.family}} daddr {{.listenHost}} {{.connType}} dport {{.listenPort}} dnat to {{.connectDest}}
	{{- end}}
}

chain pstrt{{.chainSeparator}}{{.deviceLabel}} {
	type nat hook postrouting priority 100; policy accept;
	{{- range .rules}}
	{{.family}} saddr {{.connectHost}} {{.family}} daddr {{.connectHost}} {{.connType}} dport {{.connectPort}} masquerade
	{{- end}}
}
`))

// nftablesInstanceBridgeFilter defines the rules needed for MAC, IPv4 and IPv6 bridge security filtering.
// To prevent instances from using IPs that are different from their assigned IPs we use ARP and NDP filtering
// to prevent neighbour advertisements that are not allowed. However in order for DHCPv4 & DHCPv6 to work back to
// the LXD host we need to allow DHCPv4 inbound and for IPv6 we need to allow IPv6 Router Solicitation and DHPCv6.
// Nftables doesn't support the equivalent of "arp saddr" and "arp saddr ether" at this time so in order to filter
// NDP advertisements that come from the genuine Ethernet MAC address but have a spoofed NDP source MAC/IP adddress
// we need to use manual header offset extraction. This also drops IPv6 router advertisements from instance.
// If IP filtering is enabled, this also drops unwanted ethernet frames.
var nftablesInstanceBridgeFilter = template.Must(template.New("nftablesInstanceBridgeFilter").Parse(`
chain in{{.chainSeparator}}{{.deviceLabel}} {
	type filter hook input priority -200; policy accept;
	iifname "{{.hostName}}" ether saddr != {{.hwAddr}} drop
	iifname "{{.hostName}}" ether type arp arp saddr ether != {{.hwAddr}} drop
	iifname "{{.hostName}}" ether type ip6 icmpv6 type 136 @nh,528,48 != {{.hwAddrHex}} drop
	{{if .ipv4FilterAll -}}
	iifname "{{.hostName}}" ether type arp drop
	iifname "{{.hostName}}" ether type ip drop
	{{- end}}
	{{if .ipv4Addr -}}
	iifname "{{.hostName}}" ether type arp arp saddr ip != {{.ipv4Addr}} drop
	iifname "{{.hostName}}" ether type ip ip saddr 0.0.0.0 ip daddr 255.255.255.255 udp dport 67 accept
	iifname "{{.hostName}}" ether type ip ip saddr != {{.ipv4Addr}} drop
	{{- end}}
	{{if .ipv6FilterAll -}}
	iifname "{{.hostName}}" ether type ip6 drop
	{{- end}}
	{{if .ipv6Addr -}}
	iifname "{{.hostName}}" ether type ip6 ip6 saddr fe80::/10 ip6 daddr ff02::1:2 udp dport 547 accept
	iifname "{{.hostName}}" ether type ip6 ip6 saddr fe80::/10 ip6 daddr ff02::2 icmpv6 type 133 accept
	iifname "{{.hostName}}" ether type ip6 icmpv6 type 136 @nh,384,128 != {{.ipv6AddrHex}} drop
	iifname "{{.hostName}}" ether type ip6 ip6 saddr != {{.ipv6Addr}} drop
	iifname "{{.hostName}}" ether type ip6 icmpv6 type 134 drop
	{{- end}}
	{{if .filterUnwantedFrames -}}
	iifname "{{.hostName}}" ether type != {arp, ip, ip6} drop
	{{- end}}
}

chain fwd{{.chainSeparator}}{{.deviceLabel}} {
	type filter hook forward priority -200; policy accept;
	iifname "{{.hostName}}" ether saddr != {{.hwAddr}} drop
	iifname "{{.hostName}}" ether type arp arp saddr ether != {{.hwAddr}} drop
	iifname "{{.hostName}}" ether type ip6 icmpv6 type 136 @nh,528,48 != {{.hwAddrHex}} drop
	{{if .ipv4FilterAll -}}
	iifname "{{.hostName}}" ether type arp drop
	iifname "{{.hostName}}" ether type ip drop
	{{- end}}
	{{if .ipv4Addr -}}
	iifname "{{.hostName}}" ether type arp arp saddr ip != {{.ipv4Addr}} drop
	iifname "{{.hostName}}" ether type ip ip saddr != {{.ipv4Addr}} drop
	{{- end}}
	{{if .ipv6FilterAll -}}
	iifname "{{.hostName}}" ether type ip6 drop
	{{- end}}
	{{if .ipv6Addr -}}
	iifname "{{.hostName}}" ether type ip6 ip6 saddr != {{.ipv6Addr}} drop
	iifname "{{.hostName}}" ether type ip6 icmpv6 type 136 @nh,384,128 != {{.ipv6AddrHex}} drop
	iifname "{{.hostName}}" ether type ip6 icmpv6 type 134 drop
	{{- end}}
	{{if .filterUnwantedFrames -}}
	iifname "{{.hostName}}" ether type != {arp, ip, ip6} drop
	{{- end}}
}
`))

// nftablesInstanceRPFilter defines the rules to perform reverse path filtering.
var nftablesInstanceRPFilter = template.Must(template.New("nftablesInstanceRPFilter").Parse(`
chain prert{{.chainSeparator}}{{.deviceLabel}} {
	type filter hook prerouting priority -300; policy accept;
	iif "{{.hostName}}" fib saddr . iif oif missing drop
}
`))

// nftablesACLBridgeRulesSetup defines the chains, port groups and rules needed to appply ACLs to a network.
var nftablesACLBridgeRulesSetup = template.Must(template.New("nftablesACLBridgeRules").Parse(`
# Create table and network chains (noop if exists), so that flush doesn't fail if don't exist.
add table {{.family}} {{.namespace}}
add chain {{.family}} {{.namespace}} acl{{.chainSeparator}}extout{{.chainSeparator}}{{.networkName}} {type filter hook input priority 0; policy accept;}
add chain {{.family}} {{.namespace}} acl{{.chainSeparator}}extin{{.chainSeparator}}{{.networkName}} {type filter hook output priority 0; policy accept;}
add chain {{.family}} {{.namespace}} acl{{.chainSeparator}}int{{.chainSeparator}}{{.networkName}} {type filter hook forward priority 0; policy accept;}

# Flush any existing rules in our network chains.
flush chain {{.family}} {{.namespace}} acl{{.chainSeparator}}extout{{.chainSeparator}}{{.networkName}}
flush chain {{.family}} {{.namespace}} acl{{.chainSeparator}}extin{{.chainSeparator}}{{.networkName}}
flush chain {{.family}} {{.namespace}} acl{{.chainSeparator}}int{{.chainSeparator}}{{.networkName}}

# Add the rules to the network chains.
table {{.family}} {{.namespace}} {
	set {{.portGroupInternal}} {
		type ifname
	}

	# Chain for traffic leaving our network via the external gateway.
	chain acl{{.chainSeparator}}extout{{.chainSeparator}}{{.networkName}} {
		type filter hook input priority 0; policy accept;

		# Ignore traffic not from a NIC port in our network.
		iifname != @{{.portGroupInternal}} accept

		# Baseline network rules.
		ct state established,related accept
		ether type arp accept
		icmpv6 type {1, 2, 3, 4, 133, 135, 136, 143} accept
		icmp type {3, 11, 12} accept
		ether type ip udp dport 67 accept
		ether type ip6 udp dport 547 accept

		{{if .intRouterIPv4 -}}
		icmp type 8 ip daddr {{.intRouterIPv4}} accept
		{{- end}}

		{{if .intRouterIPv6 -}}
		icmpv6 type 128 ip6 daddr {{.intRouterIPv6}} accept
		{{- end}}

		{{- range .dnsIPv4s}}
		ip daddr {{.}} udp dport 53 accept
		ip daddr {{.}} tcp dport 53 accept
		{{- end}}

		{{- range .dnsIPv6s}}
		ip6 daddr {{.}} udp dport 53 accept
		ip6 daddr {{.}} tcp dport 53 accept
		{{- end}}

		# Jump to NIC default external out rules.
		jump {{.chainNICDefaultExtOut}}
	}

	# Chain for traffic entering our network from the external gateway.
	chain acl{{.chainSeparator}}extin{{.chainSeparator}}{{.networkName}} {
		type filter hook output priority 0; policy accept;

		# Ignore traffic not going to a NIC port in our network.
		oifname != @{{.portGroupInternal}} accept

		# Baseline rules.
		ct state established,related accept
		ether type arp accept
		icmpv6 type {1, 2, 3, 4, 134, 135, 136, 143} accept
		icmp type {3, 11, 12} accept
		ether type ip udp dport 68 accept
		ether type ip6 udp dport 546 accept

		# Jump to NIC default rules.
		jump {{.chainNICDefault}}
	}

	# Chain for traffic going between internal NIC ports on our network.
	chain acl{{.chainSeparator}}int{{.chainSeparator}}{{.networkName}} {
		type filter hook forward priority 0; policy accept;

		# Ignore traffic not from a NIC port in our network.
		iifname != @{{.portGroupInternal}} accept

		# Baseline rules.
		ct state established,related accept
		ether type arp accept
		icmpv6 type {1, 2, 3, 4, 135, 136, 143} accept
		icmp type {3, 11, 12} accept

		# Jump to NIC default rules.
		jump {{.chainNICDefault}}
	}

	chain {{.chainNICDefault}} {
	}

	chain {{.chainNICDefaultExtOut}} {
	}
}
`))

// nftablesACLBridgeRulesDelete removes the chains, port groups and rules needed to appply ACLs to a network.
var nftablesACLBridgeRulesDelete = template.Must(template.New("nftablesACLBridgeRules").Parse(`
# Create table and network chains (noop if exists), so that deletion doesn't fail if don't exist.
add table {{.family}} {{.namespace}}
add chain {{.family}} {{.namespace}} acl{{.chainSeparator}}extout{{.chainSeparator}}{{.networkName}} {type filter hook input priority 0; policy accept;}
add chain {{.family}} {{.namespace}} acl{{.chainSeparator}}extin{{.chainSeparator}}{{.networkName}} {type filter hook output priority 0; policy accept;}
add chain {{.family}} {{.namespace}} acl{{.chainSeparator}}int{{.chainSeparator}}{{.networkName}} {type filter hook forward priority 0; policy accept;}
add chain {{.family}} {{.namespace}} {{.chainNICDefault}}
add chain {{.family}} {{.namespace}} {{.chainNICDefaultExtOut}}

# Delete chains.
delete chain {{.family}} {{.namespace}} acl{{.chainSeparator}}extout{{.chainSeparator}}{{.networkName}}
delete chain {{.family}} {{.namespace}} acl{{.chainSeparator}}extin{{.chainSeparator}}{{.networkName}}
delete chain {{.family}} {{.namespace}} acl{{.chainSeparator}}int{{.chainSeparator}}{{.networkName}}
delete chain {{.family}} {{.namespace}} {{.chainNICDefault}}
delete chain {{.family}} {{.namespace}} {{.chainNICDefaultExtOut}}

# Delete internal port group.
add set {{.family}} {{.namespace}} {{.portGroupInternal}} {type ifname;}
delete set {{.family}} {{.namespace}} {{.portGroupInternal}}
`))

// nftablesACLInstanceNICSetup adds instance NIC port to internal port group and adds default rules.
var nftablesACLInstanceNICSetup = template.Must(template.New("nftablesACLInstanceNICRulesSetup").Parse(`
{{- range .removeRules}}
delete rule {{$.family}} {{$.namespace}} {{.Chain}} handle {{.Handle}}
{{- end}}

add element {{$.family}} {{$.namespace}} {{.portGroupInternal}} {{"{"}}{{.portName}}{{"}"}}

table {{.family}} {{.namespace}} {
	chain {{.chainNICDefault}} {
		iifname {{.portName}} {{if .egressLog}}{{.egressLog}}{{end}} {{.egressAction}} comment {{.ruleComment}}
		oifname {{.portName}} {{if .ingressLog}}{{.ingressLog}}{{end}} {{.ingressAction}} comment {{.ruleComment}}
	}

	chain {{.chainNICDefaultExtOut}} {
		iifname {{.portName}} {{if .egressLog}}{{.egressLog}}{{end}} {{.externalEgressAction}} comment {{.ruleComment}}
	}
}
`))

// nftablesACLInstanceNICDelete removes instance NIC port from internal port group and deletes default rules.
var nftablesACLInstanceNICDelete = template.Must(template.New("nftablesACLInstanceNICRulesSetup").Parse(`
{{- range .removeRules}}
delete rule {{$.family}} {{$.namespace}} {{.Chain}} handle {{.Handle}}
{{- end}}

delete element {{$.family}} {{$.namespace}} {{.portGroupInternal}} {{"{"}}{{.portName}}{{"}"}}
`))
