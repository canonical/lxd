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

	{{if .ip4Action -}}
	ip version 4 oifname "{{.networkName}}" {{.ip4Action}}
	ip version 4 iifname "{{.networkName}}" {{.ip4Action}}
	{{- end}}

	{{if .ip6Action -}}
	ip6 version 6 oifname "{{.networkName}}" {{.ip6Action}}
	ip6 version 6 iifname "{{.networkName}}" {{.ip6Action}}
	{{- end}}
}
`))

var nftablesNetOutboundNAT = template.Must(template.New("nftablesNetOutboundNAT").Parse(`
chain pstrt{{.chainSeparator}}{{.networkName}} {
	type nat hook postrouting priority 100; policy accept;

	{{- range $ipFamily, $config := .rules}}
	{{if $config.SNATAddress -}}
	{{$ipFamily}} saddr {{$config.Subnet}} {{$ipFamily}} daddr != {{$config.Subnet}} snat {{$config.SNATAddress}}
	{{else -}}
	{{$ipFamily}} saddr {{$config.Subnet}} {{$ipFamily}} daddr != {{$config.Subnet}} masquerade
	{{- end}}
	{{- end}}
}
`))

var nftablesNetDHCPDNS = template.Must(template.New("nftablesNetDHCPDNS").Parse(`
chain in{{.chainSeparator}}{{.networkName}} {
	type filter hook input priority 0; policy accept;

	iifname "{{.networkName}}" tcp dport 53 accept
	iifname "{{.networkName}}" udp dport 53 accept

	{{- range .ipFamilies}}
	{{if eq . "ip" -}}
	iifname "{{$.networkName}}" udp dport 67 accept
	{{else -}}
	iifname "{{$.networkName}}" udp dport 547 accept
	{{- end}}
	{{- end}}
}

chain out{{.chainSeparator}}{{.networkName}} {
	type filter hook output priority 0; policy accept;

	oifname "{{.networkName}}" tcp sport 53 accept
	oifname "{{.networkName}}" udp sport 53 accept

	{{- range .ipFamilies}}
	{{if eq . "ip" -}}
	oifname "{{$.networkName}}" udp sport 67 accept
	{{else -}}
	oifname "{{$.networkName}}" udp sport 547 accept
	{{- end}}
	{{- end}}
}
`))

var nftablesNetProxyNAT = template.Must(template.New("nftablesNetProxyNAT").Parse(`
chain prert{{.chainSeparator}}{{.deviceLabel}} {
	type nat hook prerouting priority -100; policy accept;
	{{- range .rules}}
	{{.ipFamily}} daddr {{.listenHost}} {{.connType}} dport {{.listenPort}} dnat to {{.connectDest}}
	{{- end}}
}

chain out{{.chainSeparator}}{{.deviceLabel}} {
	type nat hook output priority -100; policy accept;
	{{- range .rules}}
	{{.ipFamily}} daddr {{.listenHost}} {{.connType}} dport {{.listenPort}} dnat to {{.connectDest}}
	{{- end}}
}

chain pstrt{{.chainSeparator}}{{.deviceLabel}} {
	type nat hook postrouting priority 100; policy accept;
	{{- range .rules}}
	{{.ipFamily}} saddr {{.connectHost}} {{.ipFamily}} daddr {{.connectHost}} {{.connType}} dport {{.connectPort}} masquerade
	{{- end}}
}
`))

var nftablesNetACLSetup = template.Must(template.New("nftablesNetACLSetup").Parse(`
add table {{.family}} {{.namespace}}
add chain {{.family}} {{.namespace}} acl{{.chainSeparator}}{{.networkName}}
add chain {{.family}} {{.namespace}} aclin{{.chainSeparator}}{{.networkName}} {type filter hook input priority filter; policy accept;}
add chain {{.family}} {{.namespace}} aclout{{.chainSeparator}}{{.networkName}} {type filter hook output priority filter; policy accept;}
add chain {{.family}} {{.namespace}} aclfwd{{.chainSeparator}}{{.networkName}} {type filter hook forward priority filter; policy accept;}
flush chain {{.family}} {{.namespace}} acl{{.chainSeparator}}{{.networkName}}
flush chain {{.family}} {{.namespace}} aclin{{.chainSeparator}}{{.networkName}}
flush chain {{.family}} {{.namespace}} aclout{{.chainSeparator}}{{.networkName}}
flush chain {{.family}} {{.namespace}} aclfwd{{.chainSeparator}}{{.networkName}}

table {{.family}} {{.namespace}} {
	chain aclin{{.chainSeparator}}{{.networkName}} {
		iifname {{.networkName}} jump acl{{.chainSeparator}}{{.networkName}}
	}

	chain aclout{{.chainSeparator}}{{.networkName}} {
		oifname {{.networkName}} jump acl{{.chainSeparator}}{{.networkName}}
	}

	chain aclfwd{{.chainSeparator}}{{.networkName}} {
		iifname {{.networkName}} jump acl{{.chainSeparator}}{{.networkName}}
		oifname {{.networkName}} jump acl{{.chainSeparator}}{{.networkName}}
	}
}
`))

var nftablesNetACLRules = template.Must(template.New("nftablesNetACLRules").Parse(`
flush chain {{.family}} {{.namespace}} acl{{.chainSeparator}}{{.networkName}}

table {{.family}} {{.namespace}} {
	chain acl{{.chainSeparator}}{{.networkName}} {
		{{- range .rules}}
		{{.}}
		{{- end}}
	}
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
