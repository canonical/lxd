package network

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mdlayher/netx/eui64"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/apparmor"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/daemon"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/warningtype"
	"github.com/canonical/lxd/lxd/dnsmasq"
	"github.com/canonical/lxd/lxd/dnsmasq/dhcpalloc"
	firewallDrivers "github.com/canonical/lxd/lxd/firewall/drivers"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/ip"
	"github.com/canonical/lxd/lxd/network/acl"
	"github.com/canonical/lxd/lxd/network/openvswitch"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/subprocess"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/lxd/warnings"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/validate"
	"github.com/canonical/lxd/shared/version"
)

// ForkdnsServersListPath defines the path that contains the forkdns server candidate file.
const ForkdnsServersListPath = "forkdns.servers"

// ForkdnsServersListFile file that contains the server candidates list.
const ForkdnsServersListFile = "servers.conf"

var forkdnsServersLock sync.Mutex

// Default MTU for bridge interface.
const bridgeMTUDefault = 1500

// bridge represents a LXD bridge network.
type bridge struct {
	common
}

// DBType returns the network type DB ID.
func (n *bridge) DBType() db.NetworkType {
	return db.NetworkTypeBridge
}

// Info returns the network driver info.
func (n *bridge) Info() Info {
	info := n.common.Info()
	info.AddressForwards = true

	return info
}

// checkClusterWideMACSafe returns whether it is safe to use the same MAC address for the bridge interface on all
// cluster nodes. It is not suitable to use a static MAC address when "bridge.external_interfaces" is non-empty and
// the bridge interface has no IPv4 or IPv6 address set. This is because in a clustered environment the same bridge
// config is applied to all nodes, and if the bridge is being used to connect multiple nodes to the same network
// segment it would cause MAC conflicts to use the same MAC on all nodes. If an IP address is specified then
// connecting multiple nodes to the same network segment would also cause IP conflicts, so if an IP is defined
// then we assume this is not being done. However if IP addresses are explicitly set to "none" and
// "bridge.external_interfaces" is set then it may not be safe to use a the same MAC address on all nodes.
func (n *bridge) checkClusterWideMACSafe(config map[string]string) error {
	// Fan mode breaks if using the same MAC address on each node.
	if config["bridge.mode"] == "fan" {
		return errors.New(`Cannot use static "bridge.hwaddr" MAC address in fan mode`)
	}

	// We can't be sure that multiple clustered nodes aren't connected to the same network segment so don't
	// use a static MAC address for the bridge interface to avoid introducing a MAC conflict.
	if config["bridge.external_interfaces"] != "" && config["ipv4.address"] == "none" && config["ipv6.address"] == "none" {
		return errors.New(`Cannot use static "bridge.hwaddr" MAC address when bridge has no IP addresses and has external interfaces set`)
	}

	return nil
}

// FillConfig fills requested config with any default values.
func (n *bridge) FillConfig(config map[string]string) error {
	// Set some default values where needed.
	if config["bridge.mode"] == "fan" {
		if config["fan.underlay_subnet"] == "" {
			config["fan.underlay_subnet"] = "auto"
		}

		// We enable NAT by default even if address is manually specified.
		if config["ipv4.nat"] == "" {
			config["ipv4.nat"] = "true"
		}
	} else {
		if config["ipv4.address"] == "" {
			config["ipv4.address"] = "auto"
		}

		if config["ipv4.address"] == "auto" && config["ipv4.nat"] == "" {
			config["ipv4.nat"] = "true"
		}

		if config["ipv6.address"] == "" {
			content, err := os.ReadFile("/proc/sys/net/ipv6/conf/default/disable_ipv6")
			if err == nil && string(content) == "0\n" {
				config["ipv6.address"] = "auto"
			}
		}

		if config["ipv6.address"] == "auto" && config["ipv6.nat"] == "" {
			config["ipv6.nat"] = "true"
		}
	}

	// Now replace any "auto" keys with generated values.
	err := n.populateAutoConfig(config)
	if err != nil {
		return fmt.Errorf("Failed generating auto config: %w", err)
	}

	return nil
}

// populateAutoConfig replaces "auto" in config with generated values.
func (n *bridge) populateAutoConfig(config map[string]string) error {
	changedConfig := false

	// Now populate "auto" values where needed.
	if config["ipv4.address"] == "auto" {
		subnet, err := randomSubnetV4()
		if err != nil {
			return err
		}

		config["ipv4.address"] = subnet
		changedConfig = true
	}

	if config["ipv6.address"] == "auto" {
		subnet, err := randomSubnetV6()
		if err != nil {
			return err
		}

		config["ipv6.address"] = subnet
		changedConfig = true
	}

	if config["fan.underlay_subnet"] == "auto" {
		subnet, _, err := DefaultGatewaySubnetV4()
		if err != nil {
			return err
		}

		config["fan.underlay_subnet"] = subnet.String()
		changedConfig = true
	}

	// Re-validate config if changed.
	if changedConfig && n.state != nil {
		return n.Validate(config)
	}

	return nil
}

// ValidateName validates network name.
func (n *bridge) ValidateName(name string) error {
	err := validate.IsInterfaceName(name)
	if err != nil {
		return err
	}

	// Apply common name validation that applies to all network types.
	return n.common.ValidateName(name)
}

// Validate network config.
func (n *bridge) Validate(config map[string]string) error {
	// Build driver specific rules dynamically.
	rules := map[string]func(value string) error{
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=bgp.peers.NAME.address)
		//
		// ---
		//  type: string
		//  condition: BGP server
		//  shortdesc: Peer address (IPv4 or IPv6)
		//  scope: global

		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=bgp.peers.NAME.asn)
		//
		// ---
		//  type: integer
		//  condition: BGP server
		//  shortdesc: Peer AS number
		//  scope: global

		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=bgp.peers.NAME.password)
		//
		// ---
		//  type: string
		//  condition: BGP server
		//  defaultdesc: (no password)
		//  required: no
		//  shortdesc: Peer session password
		//  scope: global

		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=bgp.peers.NAME.holdtime)
		// Specify the hold time in seconds.
		// ---
		//  type: integer
		//  condition: BGP server
		//  defaultdesc: `180`
		//  required: no
		//  shortdesc: Peer session hold time
		//  scope: global

		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=bgp.ipv4.nexthop)
		//
		// ---
		//  type: string
		//  condition: BGP server
		//  defaultdesc: local address
		//  shortdesc: Override the IPv4 next-hop for advertised prefixes
		//  scope: local
		"bgp.ipv4.nexthop": validate.Optional(validate.IsNetworkAddressV4),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=bgp.ipv6.nexthop)
		//
		// ---
		//  type: string
		//  condition: BGP server
		//  defaultdesc: local address
		//  shortdesc: Override the IPv6 next-hop for advertised prefixes
		//  scope: local
		"bgp.ipv6.nexthop": validate.Optional(validate.IsNetworkAddressV6),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=bridge.driver)
		// Possible values are `native` and `openvswitch`.
		// ---
		//  type: string
		//  defaultdesc: `native`
		//  shortdesc: Bridge driver
		//  scope: global
		"bridge.driver": validate.Optional(validate.IsOneOf("native", "openvswitch")),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=bridge.external_interfaces)
		// Specify a comma-separated list of unconfigured network interfaces to include in the bridge.
		// ---
		//  type: string
		//  shortdesc: Unconfigured network interfaces to include in the bridge
		//  scope: local
		"bridge.external_interfaces": validate.Optional(func(value string) error {
			for entry := range strings.SplitSeq(value, ",") {
				entry = strings.TrimSpace(entry)
				err := validate.IsInterfaceName(entry)
				if err != nil {
					return fmt.Errorf("Invalid interface name %q: %w", entry, err)
				}
			}

			return nil
		}),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=bridge.hwaddr)
		//
		// ---
		//  type: string
		//  shortdesc: MAC address for the bridge
		//  scope: global
		"bridge.hwaddr": validate.Optional(validate.IsNetworkMAC),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=bridge.mtu)
		// The default value varies depending on whether the bridge uses a tunnel or a fan setup.
		// ---
		//  type: integer
		//  defaultdesc: `1500` if `bridge.mode=standard`, `1480` if `bridge.mode=fan` and `fan.type=ipip`, or `1450` if `bridge.mode=fan` and `fan.type=vxlan`
		//  shortdesc: Bridge MTU
		//  scope: global
		"bridge.mtu": validate.Optional(validate.IsNetworkMTU),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=bridge.mode)
		// Possible values are `standard` and `fan`.
		// ---
		//  type: string
		//  defaultdesc: `standard`
		//  shortdesc: Bridge operation mode
		//  scope: global
		"bridge.mode": validate.Optional(validate.IsOneOf("standard", "fan")),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=fan.overlay_subnet)
		// Use CIDR notation.
		// ---
		//  type: string
		//  condition: fan mode
		//  defaultdesc: `240.0.0.0/8`
		//  shortdesc: Subnet to use as the overlay for the FAN
		//  scope: global
		"fan.overlay_subnet": validate.Optional(validate.IsNetworkV4),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=fan.underlay_subnet)
		// Use CIDR notation.
		//
		// You can set the option to `auto` to use the default gateway subnet.
		// ---
		//  type: string
		//  condition: fan mode
		//  defaultdesc: initial value on creation: `auto`
		//  shortdesc: Subnet to use as the underlay for the FAN
		//  scope: global
		"fan.underlay_subnet": validate.Optional(func(value string) error {
			if value == "auto" {
				return nil
			}

			return validate.IsNetworkV4(value)
		}),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=fan.type)
		// Possible values are `vxlan` and `ipip`.
		// ---
		//  type: string
		//  condition: fan mode
		//  defaultdesc: `vxlan`
		//  shortdesc: Tunneling type for the FAN
		//  scope: global
		"fan.type": validate.Optional(validate.IsOneOf("vxlan", "ipip")),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv4.address)
		// Use CIDR notation.
		//
		// You can set the option to `none` to turn off IPv4, or to `auto` to generate a new random unused subnet.
		// ---
		//  type: string
		//  condition: standard mode
		//  defaultdesc: initial value on creation: `auto`
		//  shortdesc: IPv4 address for the bridge
		//  scope: global
		"ipv4.address": validate.Optional(func(value string) error {
			if validate.IsOneOf("none", "auto")(value) == nil {
				return nil
			}

			return validate.IsNetworkAddressCIDRV4(value)
		}),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv4.firewall)
		//
		// ---
		//  type: bool
		//  condition: IPv4 address
		//  defaultdesc: `true`
		//  shortdesc: Whether to generate filtering firewall rules for this network
		//  scope: global
		"ipv4.firewall": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv4.nat)
		//
		// ---
		//  type: bool
		//  condition: IPv4 address
		//  defaultdesc: `false` (initial value on creation if `ipv4.address` is set to `auto`: `true`)
		//  shortdesc: Whether to use NAT for IPv4
		//  scope: global
		"ipv4.nat": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv4.nat.order)
		// Set this option to `before` to add the NAT rules before any pre-existing rules, or to `after` to add them after the pre-existing rules.
		// ---
		//  type: string
		//  condition: IPv4 address
		//  defaultdesc: `before`
		//  shortdesc: Where to add the required NAT rules
		//  scope: global
		"ipv4.nat.order": validate.Optional(validate.IsOneOf("before", "after")),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv4.nat.address)
		//
		// ---
		//  type: string
		//  condition: IPv4 address
		//  shortdesc: Source address used for outbound traffic from the bridge
		//  scope: global
		"ipv4.nat.address": validate.Optional(validate.IsNetworkAddressV4),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv4.dhcp)
		//
		// ---
		//  type: bool
		//  condition: IPv4 address
		//  defaultdesc: `true`
		//  shortdesc: Whether to allocate IPv4 addresses using DHCP
		//  scope: global
		"ipv4.dhcp": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv4.dhcp.gateway)
		//
		// ---
		//  type: string
		//  condition: IPv4 DHCP
		//  defaultdesc: IPv4 address
		//  shortdesc: Address of the gateway for the IPv4 subnet
		//  scope: global
		"ipv4.dhcp.gateway": validate.Optional(validate.IsNetworkAddressV4),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv4.dhcp.expiry)
		//
		// ---
		//  type: string
		//  condition: IPv4 DHCP
		//  defaultdesc: `1h`
		//  shortdesc: When to expire DHCP leases
		//  scope: global
		"ipv4.dhcp.expiry": validate.IsAny,
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv4.dhcp.ranges)
		// Specify a comma-separated list of IPv4 ranges in FIRST-LAST format.
		// ---
		//  type: string
		//  condition: IPv4 DHCP
		//  defaultdesc: all addresses
		//  shortdesc: IPv4 ranges to use for DHCP
		//  scope: global
		"ipv4.dhcp.ranges": validate.Optional(validate.IsListOf(validate.IsNetworkRangeV4)),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv4.routes)
		// Specify a comma-separated list of IPv4 CIDR subnets.
		// ---
		//  type: string
		//  condition: IPv4 address
		//  shortdesc: Additional IPv4 CIDR subnets to route to the bridge
		//  scope: global
		"ipv4.routes": validate.Optional(validate.IsListOf(validate.IsNetworkV4)),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv4.routing)
		//
		// ---
		//  type: bool
		//  condition: IPv4 address
		//  defaultdesc: `true`
		//  shortdesc: Whether to route IPv4 traffic in and out of the bridge
		//  scope: global
		"ipv4.routing": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv4.ovn.ranges)
		// Specify a comma-separated list of IPv4 ranges in FIRST-LAST format.
		// ---
		//  type: string
		//  shortdesc: IPv4 ranges to use for child OVN network routers
		//  scope: global
		"ipv4.ovn.ranges": validate.Optional(validate.IsListOf(validate.IsNetworkRangeV4)),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv6.address)
		// Use CIDR notation.
		//
		// You can set the option to `none` to turn off IPv6, or to `auto` to generate a new random unused subnet.
		// ---
		//  type: string
		//  condition: standard mode
		//  defaultdesc: initial value on creation: `auto`
		//  shortdesc: IPv6 address for the bridge
		//  scope: global
		"ipv6.address": validate.Optional(func(value string) error {
			if validate.IsOneOf("none", "auto")(value) == nil {
				return nil
			}

			return validate.IsNetworkAddressCIDRV6(value)
		}),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv6.firewall)
		//
		// ---
		//  type: bool
		//  condition: IPv6 DHCP
		//  defaultdesc: `true`
		//  shortdesc: Whether to generate filtering firewall rules for this network
		//  scope: global
		"ipv6.firewall": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv6.nat)
		//
		// ---
		//  type: bool
		//  condition: IPv6 address
		//  defaultdesc: `false` (initial value on creation if `ipv6.address` is set to `auto`: `true`)
		//  shortdesc: Whether to use NAT for IPv6
		//  scope: global
		"ipv6.nat": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv6.nat.order)
		// Set this option to `before` to add the NAT rules before any pre-existing rules, or to `after` to add them after the pre-existing rules.
		// ---
		//  type: string
		//  condition: IPv6 address
		//  defaultdesc: `before`
		//  shortdesc: Where to add the required NAT rules
		//  scope: global
		"ipv6.nat.order": validate.Optional(validate.IsOneOf("before", "after")),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv6.nat.address)
		//
		// ---
		//  type: string
		//  condition: IPv6 address
		//  shortdesc: Source address used for outbound traffic from the bridge
		//  scope: global
		"ipv6.nat.address": validate.Optional(validate.IsNetworkAddressV6),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv6.dhcp)
		//
		// ---
		//  type: bool
		//  condition: IPv6 address
		//  defaultdesc: `true`
		//  shortdesc: Whether to provide additional network configuration over DHCP
		//  scope: global
		"ipv6.dhcp": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv6.dhcp.expiry)
		//
		// ---
		//  type: string
		//  condition: IPv6 DHCP
		//  defaultdesc: `1h`
		//  shortdesc: When to expire DHCP leases
		//  scope: global
		"ipv6.dhcp.expiry": validate.IsAny,
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv6.dhcp.stateful)
		//
		// ---
		//  type: bool
		//  condition: IPv6 DHCP
		//  defaultdesc: `false`
		//  shortdesc: Whether to allocate IPv6 addresses using DHCP
		//  scope: global
		"ipv6.dhcp.stateful": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv6.dhcp.ranges)
		// Specify a comma-separated list of IPv6 ranges in FIRST-LAST format.
		// ---
		//  type: string
		//  condition: IPv6 stateful DHCP
		//  defaultdesc: all addresses
		//  shortdesc: IPv6 ranges to use for DHCP
		//  scope: global
		"ipv6.dhcp.ranges": validate.Optional(validate.IsListOf(validate.IsNetworkRangeV6)),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv6.routes)
		// Specify a comma-separated list of IPv6 CIDR subnets.
		// ---
		//  type: string
		//  condition: IPv6 address
		//  shortdesc: Additional IPv6 CIDR subnets to route to the bridge
		//  scope: global
		"ipv6.routes": validate.Optional(validate.IsListOf(validate.IsNetworkV6)),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv6.routing)
		//
		// ---
		//  type: bool
		//  condition: IPv6 address
		//  shortdesc: `true`
		//  shortdesc: Whether to route IPv6 traffic in and out of the bridge
		//  scope: global
		"ipv6.routing": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=ipv6.ovn.ranges)
		// Specify a comma-separated list of IPv6 ranges in FIRST-LAST format.
		// ---
		//  type: string
		//  shortdesc: IPv6 ranges to use for child OVN network routers
		//  scope: global
		"ipv6.ovn.ranges": validate.Optional(validate.IsListOf(validate.IsNetworkRangeV6)),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=dns.domain)
		//
		// ---
		//  type: string
		//  defaultdesc: `lxd`
		//  shortdesc: Domain to advertise to DHCP clients and use for DNS resolution
		//  scope: global
		"dns.domain": validate.IsAny,
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=dns.mode)
		// Possible values are `none` for no DNS record, `managed` for LXD-generated static records, and `dynamic` for client-generated records.
		// ---
		//  type: string
		//  defaultdesc: `managed`
		//  shortdesc: DNS registration mode
		//  scope: global
		"dns.mode": validate.Optional(validate.IsOneOf("dynamic", "managed", "none")),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=dns.search)
		// Specify a comma-separated list of domains.
		// ---
		//  type: string
		//  defaultdesc: `dns.domain` value
		//  shortdesc: Full domain search list
		//  scope: global
		"dns.search": validate.IsAny,
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=dns.zone.forward)
		// Specify a comma-separated list of DNS zone names.
		// ---
		//  type: string
		//  shortdesc: DNS zone names for forward DNS records
		//  scope: global
		"dns.zone.forward": validate.IsAny,
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=dns.zone.reverse.ipv4)
		//
		// ---
		//  type: string
		//  shortdesc: DNS zone name for IPv4 reverse DNS records
		//  scope: global
		"dns.zone.reverse.ipv4": validate.IsAny,
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=dns.zone.reverse.ipv6)
		//
		// ---
		//  type: string
		//  shortdesc: DNS zone name for IPv6 reverse DNS records
		//  scope: global
		"dns.zone.reverse.ipv6": validate.IsAny,
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=raw.dnsmasq)
		//
		// ---
		//  type: string
		//  shortdesc: Additional `dnsmasq` configuration to append to the configuration file
		//  scope: global
		"raw.dnsmasq": validate.IsAny,
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=maas.subnet.ipv4)
		//
		// ---
		//  type: string
		//  condition: IPv4 address; using the `network` property on the NIC
		//  shortdesc: `true`
		//  shortdesc: MAAS IPv4 subnet to register instances in
		//  scope: global
		"maas.subnet.ipv4": validate.IsAny,
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=maas.subnet.ipv6)
		//
		// ---
		//  type: string
		//  condition: IPv6 address; using the `network` property on the NIC
		//  shortdesc: `true`
		//  shortdesc: MAAS IPv6 subnet to register instances in
		//  scope: global
		"maas.subnet.ipv6": validate.IsAny,
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=security.acls)
		// Specify a comma-separated list of network ACLs.
		//
		// Also see {ref}`network-acls-bridge-limitations`.
		// ---
		//  type: string
		//  shortdesc: Network ACLs to apply to NICs connected to this network
		//  scope: global
		"security.acls": validate.IsAny,
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=security.acls.default.ingress.action)
		// The specified action is used for all ingress traffic that doesn’t match any ACL rule.
		// ---
		//  type: string
		//  condition: `security.acls`
		//  shortdesc: `reject`
		//  shortdesc: Default action to use for ingress traffic
		//  scope: global
		"security.acls.default.ingress.action": validate.Optional(validate.IsOneOf(acl.ValidActions...)),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=security.acls.default.egress.action)
		// The specified action is used for all egress traffic that doesn’t match any ACL rule.
		// ---
		//  type: string
		//  condition: `security.acls`
		//  shortdesc: `reject`
		//  shortdesc: Default action to use for egress traffic
		//  scope: global
		"security.acls.default.egress.action": validate.Optional(validate.IsOneOf(acl.ValidActions...)),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=security.acls.default.ingress.logged)
		//
		// ---
		//  type: bool
		//  condition: `security.acls`
		//  shortdesc: `false`
		//  shortdesc: Whether to log ingress traffic that doesn’t match any ACL rule
		//  scope: global
		"security.acls.default.ingress.logged": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=security.acls.default.egress.logged)
		//
		// ---
		//  type: bool
		//  condition: `security.acls`
		//  shortdesc: `false`
		//  shortdesc: Whether to log egress traffic that doesn’t match any ACL rule
		//  scope: global
		"security.acls.default.egress.logged": validate.Optional(validate.IsBool),

		// lxdmeta:generate(entities=network-bridge; group=network-conf; key=user.*)
		//
		// ---
		//  type: string
		//  shortdesc: User-provided free-form key/value pairs
		//  scope: global
	}

	// Add dynamic validation rules.
	for k := range config {
		// Tunnel keys have the remote name in their name, extract the suffix.
		if strings.HasPrefix(k, "tunnel.") {
			// Validate remote name in key.
			fields := strings.Split(k, ".")
			if len(fields) != 3 {
				return fmt.Errorf("Invalid network configuration key: %s", k)
			}

			if len(n.name)+len(fields[1]) > 14 {
				return fmt.Errorf("Network name too long for tunnel interface: %s-%s", n.name, fields[1])
			}

			tunnelKey := fields[2]

			// Add the correct validation rule for the dynamic field based on last part of key.
			switch tunnelKey {
			case "protocol":
				// lxdmeta:generate(entities=network-bridge; group=network-conf; key=tunnel.NAME.protocol)
				// Possible values are `vxlan` and `gre`.
				// ---
				//  type: string
				//  condition: standard mode
				//  shortdesc: Tunneling protocol
				rules[k] = validate.Optional(validate.IsOneOf("gre", "vxlan"))
			case "local":
				// lxdmeta:generate(entities=network-bridge; group=network-conf; key=tunnel.NAME.local)
				//
				// ---
				//  type: string
				//  condition: `gre` or `vxlan`
				//  required: not required for multicast `vxlan`
				//  shortdesc: Local address for the tunnel
				rules[k] = validate.Optional(validate.IsNetworkAddress)
			case "remote":
				// lxdmeta:generate(entities=network-bridge; group=network-conf; key=tunnel.NAME.remote)
				//
				// ---
				//  type: string
				//  condition: `gre` or `vxlan`
				//  required: not required for multicast `vxlan`
				//  shortdesc: Remote address for the tunnel
				rules[k] = validate.Optional(validate.IsNetworkAddress)
			case "port":
				// lxdmeta:generate(entities=network-bridge; group=network-conf; key=tunnel.NAME.port)
				//
				// ---
				//  type: integer
				//  condition: `vxlan`
				//  defaultdesc: `0`
				//  shortdesc: Specific port to use for the `vxlan` tunnel
				rules[k] = networkValidPort
			case "group":
				// lxdmeta:generate(entities=network-bridge; group=network-conf; key=tunnel.NAME.group)
				// This address is used if {config:option}`network-bridge-network-conf:tunnel.NAME.local` and {config:option}`network-bridge-network-conf:tunnel.NAME.remote` aren’t set.
				// ---
				//  type: string
				//  condition: `vxlan`
				//  shortdesc: `239.0.0.1`
				//  shortdesc: Multicast address for `vxlan`
				rules[k] = validate.Optional(validate.IsNetworkAddress)
			case "id":
				// lxdmeta:generate(entities=network-bridge; group=network-conf; key=tunnel.NAME.id)
				//
				// ---
				//  type: integer
				//  condition: `vxlan`
				//  shortdesc: `0`
				//  shortdesc: Specific tunnel ID to use for the `vxlan` tunnel
				rules[k] = validate.Optional(validate.IsInt64)
			case "interface":
				// lxdmeta:generate(entities=network-bridge; group=network-conf; key=tunnel.NAME.interface)
				//
				// ---
				//  type: string
				//  condition: `vxlan`
				//  shortdesc: Specific host interface to use for the tunnel
				rules[k] = validate.IsInterfaceName
			case "ttl":
				// lxdmeta:generate(entities=network-bridge; group=network-conf; key=tunnel.NAME.ttl)
				//
				// ---
				//  type: string
				//  condition: `vxlan`
				//  defaultdesc: `1`
				//  shortdesc: Specific TTL to use for multicast routing topologies
				rules[k] = validate.Optional(validate.IsUint8)
			}
		}
	}

	// Add the BGP validation rules.
	bgpRules, err := n.bgpValidationRules(config)
	if err != nil {
		return err
	}

	maps.Copy(rules, bgpRules)

	// Validate the configuration.
	err = n.validate(config, rules)
	if err != nil {
		return err
	}

	// Perform composite key checks after per-key validation.

	// Validate DNS zone names.
	err = n.validateZoneNames(config)
	if err != nil {
		return err
	}

	// Check that ipv4.routes and ipv6.routes contain the routes for existing OVN network
	// forwards and load balancers.
	err = n.validateRoutes(config)
	if err != nil {
		return err
	}

	// Validate network name when used in fan mode.
	bridgeMode := config["bridge.mode"]
	if bridgeMode == "fan" && len(n.name) > 11 {
		return errors.New("Network name too long to use with the FAN (must be 11 characters or less)")
	}

	bridgeModeOptions := []string{"ipv4.dhcp.expiry", "ipv4.firewall", "ipv4.nat", "ipv4.nat.order"}
	for k, v := range config {
		key := k
		// Bridge mode checks
		if bridgeMode == "fan" && strings.HasPrefix(key, "ipv4.") && !slices.Contains(bridgeModeOptions, key) && v != "" {
			return errors.New("IPv4 configuration may not be set when in 'fan' mode")
		}

		if bridgeMode == "fan" && strings.HasPrefix(key, "ipv6.") && v != "" {
			return errors.New("IPv6 configuration may not be set when in 'fan' mode")
		}

		if bridgeMode != "fan" && strings.HasPrefix(key, "fan.") && v != "" {
			return errors.New("FAN configuration may only be set when in 'fan' mode")
		}

		// MTU checks
		if key == "bridge.mtu" && v != "" {
			mtu, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("Invalid value for an integer: %s", v)
			}

			ipv6 := config["ipv6.address"]
			if ipv6 != "" && ipv6 != "none" && mtu < 1280 {
				return errors.New("The minimum MTU for an IPv6 network is 1280")
			}

			ipv4 := config["ipv4.address"]
			if ipv4 != "" && ipv4 != "none" && mtu < 68 {
				return errors.New("The minimum MTU for an IPv4 network is 68")
			}

			if config["bridge.mode"] == "fan" {
				if config["fan.type"] == "ipip" {
					if mtu > 1480 {
						return errors.New("Maximum MTU for an IPIP FAN bridge is 1480")
					}
				} else {
					if mtu > 1450 {
						return errors.New("Maximum MTU for a VXLAN FAN bridge is 1450")
					}
				}
			}
		}
	}

	// Check using same MAC address on every cluster node is safe.
	if config["bridge.hwaddr"] != "" {
		err = n.checkClusterWideMACSafe(config)
		if err != nil {
			return err
		}
	}

	// Check IPv4 OVN ranges.
	if config["ipv4.ovn.ranges"] != "" && shared.IsTrueOrEmpty(config["ipv4.dhcp"]) {
		dhcpSubnet := n.DHCPv4Subnet()
		allowedNets := []*net.IPNet{}

		if dhcpSubnet != nil {
			if config["ipv4.dhcp.ranges"] == "" {
				return errors.New(`"ipv4.ovn.ranges" must be used in conjunction with non-overlapping "ipv4.dhcp.ranges" when DHCPv4 is enabled`)
			}

			allowedNets = append(allowedNets, dhcpSubnet)
		}

		ovnRanges, err := shared.ParseIPRanges(config["ipv4.ovn.ranges"], allowedNets...)
		if err != nil {
			return fmt.Errorf("Failed parsing ipv4.ovn.ranges: %w", err)
		}

		dhcpRanges, err := shared.ParseIPRanges(config["ipv4.dhcp.ranges"], allowedNets...)
		if err != nil {
			return fmt.Errorf("Failed parsing ipv4.dhcp.ranges: %w", err)
		}

		for _, ovnRange := range ovnRanges {
			if slices.ContainsFunc(dhcpRanges, ovnRange.Overlaps) {
				return fmt.Errorf(`The range specified in "ipv4.ovn.ranges" (%q) cannot overlap with "ipv4.dhcp.ranges"`, ovnRange)
			}
		}
	}

	// Check IPv6 OVN ranges.
	if config["ipv6.ovn.ranges"] != "" && shared.IsTrueOrEmpty(config["ipv6.dhcp"]) {
		dhcpSubnet := n.DHCPv6Subnet()
		allowedNets := []*net.IPNet{}

		if dhcpSubnet != nil {
			if config["ipv6.dhcp.ranges"] == "" && shared.IsTrue(config["ipv6.dhcp.stateful"]) {
				return errors.New(`"ipv6.ovn.ranges" must be used in conjunction with non-overlapping "ipv6.dhcp.ranges" when stateful DHCPv6 is enabled`)
			}

			allowedNets = append(allowedNets, dhcpSubnet)
		}

		ovnRanges, err := shared.ParseIPRanges(config["ipv6.ovn.ranges"], allowedNets...)
		if err != nil {
			return fmt.Errorf("Failed parsing ipv6.ovn.ranges: %w", err)
		}

		// If stateful DHCPv6 is enabled, check OVN ranges don't overlap with DHCPv6 stateful ranges.
		// Otherwise SLAAC will be being used to generate client IPs and predefined ranges aren't used.
		if dhcpSubnet != nil && shared.IsTrue(config["ipv6.dhcp.stateful"]) {
			dhcpRanges, err := shared.ParseIPRanges(config["ipv6.dhcp.ranges"], allowedNets...)
			if err != nil {
				return fmt.Errorf("Failed parsing ipv6.dhcp.ranges: %w", err)
			}

			for _, ovnRange := range ovnRanges {
				if slices.ContainsFunc(dhcpRanges, ovnRange.Overlaps) {
					return fmt.Errorf(`The range specified in "ipv6.ovn.ranges" (%q) cannot overlap with "ipv6.dhcp.ranges"`, ovnRange)
				}
			}
		}
	}

	// Check Security ACLs are supported and exist.
	if config["security.acls"] != "" {
		err = acl.Exists(n.state, n.Project(), shared.SplitNTrimSpace(config["security.acls"], ",", -1, true)...)
		if err != nil {
			return err
		}
	}

	return nil
}

// Create checks whether the bridge interface name is used already.
func (n *bridge) Create(clientType request.ClientType) error {
	n.logger.Debug("Create", logger.Ctx{"clientType": clientType, "config": n.config})

	if InterfaceExists(n.name) {
		return fmt.Errorf("Network interface %q already exists", n.name)
	}

	return nil
}

// isRunning returns whether the network is up.
func (n *bridge) isRunning() bool {
	return InterfaceExists(n.name)
}

// Delete deletes a network.
func (n *bridge) Delete(clientType request.ClientType) error {
	n.logger.Debug("Delete", logger.Ctx{"clientType": clientType})

	if n.isRunning() {
		err := n.Stop()
		if err != nil {
			return err
		}
	}

	// Delete apparmor profiles.
	err := apparmor.NetworkDelete(n.state.OS, n)
	if err != nil {
		return err
	}

	return n.delete()
}

// Rename renames a network.
func (n *bridge) Rename(newName string) error {
	n.logger.Debug("Rename", logger.Ctx{"newName": newName})

	// Reject known bad names that might cause problem when dealing with paths.
	err := n.ValidateName(newName)
	if err != nil {
		return fmt.Errorf("Invalid network name: %q: %v", newName, err)
	}

	if InterfaceExists(newName) {
		return fmt.Errorf("Network interface %q already exists", newName)
	}

	// Bring the network down.
	if n.isRunning() {
		err := n.Stop()
		if err != nil {
			return err
		}
	}

	// Rename forkdns log file.
	forkDNSLogPath := shared.LogPath("forkdns." + n.name + ".log")
	if shared.PathExists(forkDNSLogPath) {
		err := os.Rename(forkDNSLogPath, shared.LogPath("forkdns."+newName+".log"))
		if err != nil {
			return err
		}
	}

	// Rename common steps.
	err = n.rename(newName)
	if err != nil {
		return err
	}

	// Bring the network up.
	err = n.Start()
	if err != nil {
		return err
	}

	return nil
}

// Start starts the network.
func (n *bridge) Start() error {
	n.logger.Debug("Start")

	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() { n.setUnavailable() })

	err := n.setup(nil)
	if err != nil {
		return err
	}

	revert.Success()

	// Ensure network is marked as available now its started.
	n.setAvailable()

	return nil
}

func (n *bridge) getDnsmasqArgs(bridge *ip.Bridge) ([]string, error) {
	dnsmasqCmd := []string{"--keep-in-foreground", "--strict-order", "--bind-interfaces",
		"--except-interface=lo",
		"--pid-file=", // Disable attempt at writing a PID file.
		"--no-ping",   // --no-ping is very important to prevent delays to lease file updates.
		"--interface=" + n.name}

	dnsmasqVersion, err := dnsmasq.GetVersion()
	if err != nil {
		return nil, err
	}

	// --dhcp-rapid-commit option is only supported on >2.79.
	minVer, _ := version.NewDottedVersion("2.79")
	if dnsmasqVersion.Compare(minVer) > 0 {
		dnsmasqCmd = append(dnsmasqCmd, "--dhcp-rapid-commit")
	}

	// --no-negcache option is only supported on >2.47.
	minVer, _ = version.NewDottedVersion("2.47")
	if dnsmasqVersion.Compare(minVer) > 0 {
		dnsmasqCmd = append(dnsmasqCmd, "--no-negcache")
	}

	if !daemon.Debug {
		// --quiet options are only supported on >2.67.
		minVer, _ := version.NewDottedVersion("2.67")

		if dnsmasqVersion.Compare(minVer) > 0 {
			dnsmasqCmd = append(dnsmasqCmd, "--quiet-dhcp", "--quiet-dhcp6", "--quiet-ra")
		}
	}

	// --dhcp-ignore-clid option is only supported on >2.81.
	// We want this to avoid duplicate IPs assigned to VM copies.
	// The issue is that, while LXD updates the UUID on VM copy, cloud-init doesn't update machine-id in the new instance,
	// and the same machine-id with the same link name in VM leads to the same client-id.
	// So we ask dnsmasq to use MAC instead.
	minVer, _ = version.NewDottedVersion("2.81")
	if dnsmasqVersion.Compare(minVer) > 0 {
		dnsmasqCmd = append(dnsmasqCmd, "--dhcp-ignore-clid")
	}

	// Configure IPv4.
	if !slices.Contains([]string{"", "none"}, n.config["ipv4.address"]) {
		var subnet *net.IPNet

		// Parse the subnet.
		ipv4Address, subnet, err := net.ParseCIDR(n.config["ipv4.address"])
		if err != nil {
			return nil, fmt.Errorf("Failed parsing ipv4.address: %w", err)
		}

		// Update the dnsmasq config.
		dnsmasqCmd = append(dnsmasqCmd, "--listen-address="+ipv4Address.String())
		if n.DHCPv4Subnet() != nil {
			if !slices.Contains(dnsmasqCmd, "--dhcp-no-override") {
				dnsmasqCmd = append(dnsmasqCmd, "--dhcp-no-override", "--dhcp-authoritative", "--dhcp-leasefile="+shared.VarPath("networks", n.name, "dnsmasq.leases"), "--dhcp-hostsfile="+shared.VarPath("networks", n.name, "dnsmasq.hosts"))
			}

			if n.config["ipv4.dhcp.gateway"] != "" {
				dnsmasqCmd = append(dnsmasqCmd, "--dhcp-option-force=3,"+n.config["ipv4.dhcp.gateway"])
			}

			if bridge.MTU != bridgeMTUDefault {
				dnsmasqCmd = append(dnsmasqCmd, fmt.Sprintf("--dhcp-option-force=26,%d", bridge.MTU))
			}

			dnsSearch := n.config["dns.search"]
			if dnsSearch != "" {
				dnsmasqCmd = append(dnsmasqCmd, "--dhcp-option-force=119,"+strings.Trim(dnsSearch, " "))
			}

			expiry := "1h"
			if n.config["ipv4.dhcp.expiry"] != "" {
				expiry = n.config["ipv4.dhcp.expiry"]
			}

			if n.config["ipv4.dhcp.ranges"] != "" {
				for dhcpRange := range strings.SplitSeq(n.config["ipv4.dhcp.ranges"], ",") {
					dhcpRange = strings.TrimSpace(dhcpRange)
					dnsmasqCmd = append(dnsmasqCmd, "--dhcp-range", fmt.Sprintf("%s,%s", strings.ReplaceAll(dhcpRange, "-", ","), expiry))
				}
			} else {
				dnsmasqCmd = append(dnsmasqCmd, "--dhcp-range", fmt.Sprintf("%s,%s,%s", dhcpalloc.GetIP(subnet, 2).String(), dhcpalloc.GetIP(subnet, -2).String(), expiry))
			}
		}
	}

	// Configure IPv6.
	if !slices.Contains([]string{"", "none"}, n.config["ipv6.address"]) {
		// Parse the subnet.
		ipv6Address, subnet, err := net.ParseCIDR(n.config["ipv6.address"])
		if err != nil {
			return nil, fmt.Errorf("Failed parsing ipv6.address: %w", err)
		}

		subnetSize, _ := subnet.Mask.Size()

		// Update the dnsmasq config.
		dnsmasqCmd = append(dnsmasqCmd, "--listen-address="+ipv6Address.String(), "--enable-ra")
		if n.DHCPv6Subnet() != nil {
			// Build DHCP configuration.
			if !slices.Contains(dnsmasqCmd, "--dhcp-no-override") {
				dnsmasqCmd = append(dnsmasqCmd, "--dhcp-no-override", "--dhcp-authoritative", "--dhcp-leasefile="+shared.VarPath("networks", n.name, "dnsmasq.leases"), "--dhcp-hostsfile="+shared.VarPath("networks", n.name, "dnsmasq.hosts"))
			}

			expiry := "1h"
			if n.config["ipv6.dhcp.expiry"] != "" {
				expiry = n.config["ipv6.dhcp.expiry"]
			}

			if shared.IsTrue(n.config["ipv6.dhcp.stateful"]) {
				if n.config["ipv6.dhcp.ranges"] != "" {
					for dhcpRange := range strings.SplitSeq(n.config["ipv6.dhcp.ranges"], ",") {
						dhcpRange = strings.TrimSpace(dhcpRange)
						dnsmasqCmd = append(dnsmasqCmd, "--dhcp-range", fmt.Sprintf("%s,%d,%s", strings.ReplaceAll(dhcpRange, "-", ","), subnetSize, expiry))
					}
				} else {
					dnsmasqCmd = append(dnsmasqCmd, "--dhcp-range", fmt.Sprintf("%s,%s,%d,%s", dhcpalloc.GetIP(subnet, 2), dhcpalloc.GetIP(subnet, -1), subnetSize, expiry))
				}
			} else {
				dnsmasqCmd = append(dnsmasqCmd, "--dhcp-range", fmt.Sprintf("::,constructor:%s,ra-stateless,ra-names", n.name))
			}
		} else {
			dnsmasqCmd = append(dnsmasqCmd, "--dhcp-range", fmt.Sprintf("::,constructor:%s,ra-only", n.name))
		}
	}

	return dnsmasqCmd, nil
}

func (n *bridge) addDnsmasqFanArgs(args []string, address string, fanMTU uint32) ([]string, error) {
	// Parse the host subnet.
	_, hostSubnet, err := net.ParseCIDR(address + "/24")
	if err != nil {
		return nil, fmt.Errorf("Failed parsing fan address: %w", err)
	}

	expiry := "1h"
	if n.config["ipv4.dhcp.expiry"] != "" {
		expiry = n.config["ipv4.dhcp.expiry"]
	}

	args = append(args,
		"--listen-address="+address,
		"--dhcp-no-override", "--dhcp-authoritative",
		fmt.Sprintf("--dhcp-option-force=26,%d", fanMTU),
		"--dhcp-leasefile="+shared.VarPath("networks", n.name, "dnsmasq.leases"),
		"--dhcp-hostsfile="+shared.VarPath("networks", n.name, "dnsmasq.hosts"),
		"--dhcp-range", fmt.Sprintf("%s,%s,%s", dhcpalloc.GetIP(hostSubnet, 2).String(), dhcpalloc.GetIP(hostSubnet, -2).String(), expiry))

	return args, nil
}

func (n *bridge) startDnsmasq(dnsmasqCmd []string, dnsClustered bool, dnsClusteredAddress string, overlaySubnet *net.IPNet) error {
	// Setup the dnsmasq domain.
	dnsDomain := n.config["dns.domain"]
	if dnsDomain == "" {
		dnsDomain = "lxd"
	}

	if n.config["dns.mode"] != "none" {
		dnsmasqCmd = append(dnsmasqCmd, "-s", dnsDomain)
		dnsmasqCmd = append(dnsmasqCmd, "--interface-name", fmt.Sprintf("_gateway.%s,%s", dnsDomain, n.name))

		if dnsClustered {
			dnsmasqCmd = append(dnsmasqCmd, "-S", fmt.Sprintf("/%s/%s#1053", dnsDomain, dnsClusteredAddress))
			dnsmasqCmd = append(dnsmasqCmd, fmt.Sprintf("--rev-server=%s,%s#1053", overlaySubnet, dnsClusteredAddress))
		} else {
			dnsmasqCmd = append(dnsmasqCmd, "-S", fmt.Sprintf("/%s/", dnsDomain))
		}
	}

	// Create a config file to contain additional config (and to prevent dnsmasq from reading /etc/dnsmasq.conf)
	err := os.WriteFile(shared.VarPath("networks", n.name, "dnsmasq.raw"), []byte(n.config["raw.dnsmasq"]+"\n"), 0644)
	if err != nil {
		return err
	}

	dnsmasqCmd = append(dnsmasqCmd, "--conf-file="+shared.VarPath("networks", n.name, "dnsmasq.raw"))

	// Attempt to drop privileges.
	if n.state.OS.UnprivUser != "" {
		dnsmasqCmd = append(dnsmasqCmd, "-u", n.state.OS.UnprivUser)
	}

	if n.state.OS.UnprivGroup != "" {
		dnsmasqCmd = append(dnsmasqCmd, "-g", n.state.OS.UnprivGroup)
	}

	// Create DHCP hosts directory.
	dnsmasqHostDir := shared.VarPath("networks", n.name, "dnsmasq.hosts")
	if !shared.PathExists(dnsmasqHostDir) {
		err = os.MkdirAll(dnsmasqHostDir, 0755)
		if err != nil {
			return err
		}
	}

	// Check for dnsmasq.
	_, err = exec.LookPath("dnsmasq")
	if err != nil {
		return errors.New("dnsmasq is required for LXD managed bridges")
	}

	// Update the static leases.
	err = UpdateDNSMasqStatic(n.state, n.name)
	if err != nil {
		return err
	}

	// Create subprocess object dnsmasq.
	command := "dnsmasq"
	dnsmasqLogPath := shared.LogPath(fmt.Sprintf("dnsmasq.%s.log", n.name))
	p, err := subprocess.NewProcess(command, dnsmasqCmd, "", dnsmasqLogPath)
	if err != nil {
		return fmt.Errorf("Failed to create subprocess: %s", err)
	}

	// Apply AppArmor confinement.
	if n.config["raw.dnsmasq"] == "" {
		p.SetApparmor(apparmor.DnsmasqProfileName(n))

		err = warnings.ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(n.state.DB.Cluster, n.project, warningtype.AppArmorDisabledDueToRawDnsmasq, entity.TypeNetwork, int(n.id))
		if err != nil {
			n.logger.Warn("Failed to resolve warning", logger.Ctx{"err": err})
		}
	} else {
		n.logger.Warn("Skipping AppArmor for dnsmasq due to raw.dnsmasq being set", logger.Ctx{"name": n.name})

		err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpsertWarningLocalNode(ctx, n.project, entity.TypeNetwork, int(n.id), warningtype.AppArmorDisabledDueToRawDnsmasq, "")
		})
		if err != nil {
			n.logger.Warn("Failed to create warning", logger.Ctx{"err": err})
		}
	}

	// Start dnsmasq.
	err = p.Start(context.Background())
	if err != nil {
		return fmt.Errorf("Failed to run: %s %s: %w", command, strings.Join(dnsmasqCmd, " "), err)
	}

	// Check dnsmasq started OK.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Millisecond*time.Duration(500)))
	_, err = p.Wait(ctx)
	cancel()

	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		stderr, _ := os.ReadFile(dnsmasqLogPath)

		return fmt.Errorf("The DNS and DHCP service exited prematurely: %w (%q)", err, strings.TrimSpace(string(stderr)))
	}

	err = p.Save(shared.VarPath("networks", n.name, "dnsmasq.pid"))
	if err != nil {
		// Kill Process if started, but could not save the file.
		err2 := p.Stop()
		if err2 != nil {
			return fmt.Errorf("Could not kill subprocess while handling saving error: %s: %s", err, err2)
		}

		return fmt.Errorf("Failed to save subprocess details: %s", err)
	}

	return nil
}

// setup restarts the network.
func (n *bridge) setup(oldConfig map[string]string) error {
	// If we are in mock mode, just no-op.
	if n.state.OS.MockMode {
		return nil
	}

	n.logger.Debug("Setting up network")

	revert := revert.New()
	defer revert.Fail()

	// Create directory.
	networkDir := shared.VarPath("networks", n.name)
	if !shared.PathExists(networkDir) {
		err := os.MkdirAll(networkDir, 0711)
		if err != nil {
			return err
		}
	}

	var err error

	// Build up the bridge interface's settings.
	bridge := ip.Bridge{
		Link: ip.Link{
			Name: n.name,
			MTU:  bridgeMTUDefault,
		},
	}

	// Get a list of tunnels.
	tunnels := n.getTunnels()

	// Decide the MTU for the bridge interface.
	if n.config["bridge.mtu"] != "" {
		mtuInt, err := strconv.ParseUint(n.config["bridge.mtu"], 10, 32)
		if err != nil {
			return fmt.Errorf("Invalid MTU %q: %w", n.config["bridge.mtu"], err)
		}

		bridge.MTU = uint32(mtuInt)
	} else if len(tunnels) > 0 {
		bridge.MTU = 1400
	} else if n.config["bridge.mode"] == "fan" {
		if n.config["fan.type"] == "ipip" {
			bridge.MTU = 1480
		} else {
			bridge.MTU = 1450
		}
	}

	// Decide the MAC address of bridge interface.
	if n.config["bridge.hwaddr"] != "" {
		bridge.Address, err = net.ParseMAC(n.config["bridge.hwaddr"])
		if err != nil {
			return fmt.Errorf("Failed parsing MAC address %q: %w", n.config["bridge.hwaddr"], err)
		}
	} else {
		// If no cluster wide static MAC address set, then generate one.
		var seedNodeID int64

		if n.checkClusterWideMACSafe(n.config) != nil {
			// If not safe to use a cluster wide MAC or in in fan mode, then use cluster node's ID to
			// generate a stable per-node & network derived random MAC.
			seedNodeID = n.state.DB.Cluster.GetNodeID()
		} else {
			// If safe to use a cluster wide MAC, then use a static cluster node of 0 to generate a
			// stable per-network derived random MAC.
			seedNodeID = 0
		}

		// Load server certificate. This is needs to be the same certificate for all nodes in a cluster.
		cert, err := util.LoadCert(n.state.OS.VarDir)
		if err != nil {
			return err
		}

		// Generate the random seed, this uses the server certificate fingerprint (to ensure that multiple
		// standalone nodes with the same network ID connected to the same external network don't generate
		// the same MAC for their networks). It relies on the certificate being the same for all nodes in a
		// cluster to allow the same MAC to be generated on each bridge interface in the network when
		// seedNodeID is 0 (when safe to do so).
		seed := fmt.Sprintf("%s.%d.%d", cert.Fingerprint(), seedNodeID, n.ID())
		r, err := util.GetStableRandomGenerator(seed)
		if err != nil {
			return fmt.Errorf("Failed generating stable random bridge MAC: %w", err)
		}

		randomHwaddr := randomHwaddr(r)
		bridge.Address, err = net.ParseMAC(randomHwaddr)
		if err != nil {
			return fmt.Errorf("Failed parsing MAC address %q: %w", randomHwaddr, err)
		}

		n.logger.Debug("Stable MAC generated", logger.Ctx{"seed": seed, "hwAddr": bridge.Address.String()})
	}

	// Create the bridge interface if doesn't exist.
	if !n.isRunning() {
		if n.config["bridge.driver"] == "openvswitch" {
			ovs := openvswitch.NewOVS()
			if !ovs.Installed() {
				return errors.New("Open vSwitch isn't installed on this system")
			}

			// Add and configure the interface in one operation to reduce the number of executions and
			// to avoid systemd-udevd from applying the default MACAddressPolicy=persistent policy.
			err := ovs.BridgeAdd(n.name, false, bridge.Address, bridge.MTU)
			if err != nil {
				return err
			}

			revert.Add(func() { _ = ovs.BridgeDelete(n.name) })
		} else {
			// Add and configure the interface in one operation to reduce the number of executions and
			// to avoid systemd-udevd from applying the default MACAddressPolicy=persistent policy.
			err := bridge.Add()
			if err != nil {
				return err
			}

			revert.Add(func() { _ = bridge.Delete() })
		}
	} else {
		// If bridge already exists then re-apply settings. If we just created a bridge then we don't
		// need to do this as the settings will have been applied as part of the add operation.

		// Set the MTU on the bridge interface.
		err := bridge.SetMTU(bridge.MTU)
		if err != nil {
			return err
		}

		// Set the MAC address on the bridge interface if specified.
		if bridge.Address != nil {
			err = bridge.SetAddress(bridge.Address)
			if err != nil {
				return err
			}
		}
	}

	// IPv6 bridge configuration.
	if !slices.Contains([]string{"", "none"}, n.config["ipv6.address"]) {
		if !shared.PathExists("/proc/sys/net/ipv6") {
			return errors.New("Network has ipv6.address but kernel IPv6 support is missing")
		}

		err := util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/disable_ipv6", n.name), "0")
		if err != nil {
			return err
		}

		err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/autoconf", n.name), "0")
		if err != nil {
			return err
		}

		err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/accept_dad", n.name), "0")
		if err != nil {
			return err
		}
	} else {
		// Disable IPv6 if no address is specified. This prevents the
		// host being reachable over a guessable link-local address as well as it
		// auto-configuring an address should an instance operate an IPv6 router.
		if shared.PathExists("/proc/sys/net/ipv6") {
			err := util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/disable_ipv6", n.name), "1")
			if err != nil {
				return err
			}
		}
	}

	// Get a list of interfaces.
	ifaces, err := net.Interfaces()
	if err != nil {
		return err
	}

	// Cleanup any existing tunnel device.
	for _, iface := range ifaces {
		if strings.HasPrefix(iface.Name, n.name+"-") {
			tunLink := &ip.Link{Name: iface.Name}
			err = tunLink.Delete()
			if err != nil {
				return err
			}
		}
	}

	// Attempt to add a dummy device to the bridge to force the MTU.
	if bridge.MTU != bridgeMTUDefault && n.config["bridge.driver"] != "openvswitch" {
		dummy := &ip.Dummy{
			Link: ip.Link{
				Name: n.name + "-mtu",
				MTU:  bridge.MTU,
			},
		}

		err = dummy.Add()
		if err == nil {
			revert.Add(func() { _ = dummy.Delete() })
			err = dummy.SetUp()
			if err == nil {
				_ = AttachInterface(n.name, n.name+"-mtu")
			}
		}
	}

	// Enable VLAN filtering for Linux bridges.
	if n.config["bridge.driver"] != "openvswitch" {
		// Enable filtering.
		err = BridgeVLANFilterSetStatus(n.name, "1")
		if err != nil {
			n.logger.Warn(fmt.Sprintf("Failed enabling VLAN filtering: %v", err))
		}
	}

	// Bring it up.
	err = bridge.SetUp()
	if err != nil {
		return err
	}

	// Add any listed existing external interface.
	if n.config["bridge.external_interfaces"] != "" {
		for entry := range strings.SplitSeq(n.config["bridge.external_interfaces"], ",") {
			entry = strings.TrimSpace(entry)
			iface, err := net.InterfaceByName(entry)
			if err != nil {
				n.logger.Warn("Skipping attaching missing external interface", logger.Ctx{"interface": entry})
				continue
			}

			unused := true
			addrs, err := iface.Addrs()
			if err == nil {
				for _, addr := range addrs {
					ip, _, err := net.ParseCIDR(addr.String())
					if ip != nil && err == nil && ip.IsGlobalUnicast() {
						unused = false
						break
					}
				}
			}

			if !unused {
				return errors.New("Only unconfigured network interfaces can be bridged")
			}

			err = AttachInterface(n.name, entry)
			if err != nil {
				return err
			}
		}
	}

	// Remove any existing firewall rules.
	fwClearIPVersions := []uint{}

	if usesIPv4Firewall(n.config) || usesIPv4Firewall(oldConfig) {
		fwClearIPVersions = append(fwClearIPVersions, 4)
	}

	if usesIPv6Firewall(n.config) || usesIPv6Firewall(oldConfig) {
		fwClearIPVersions = append(fwClearIPVersions, 6)
	}

	if len(fwClearIPVersions) > 0 {
		n.logger.Debug("Clearing firewall")
		err = n.state.Firewall.NetworkClear(n.name, false, fwClearIPVersions)
		if err != nil {
			return fmt.Errorf("Failed clearing firewall: %w", err)
		}
	}

	// Initialise a new firewall option set.
	fwOpts := firewallDrivers.Opts{}

	if n.hasIPv4Firewall() {
		fwOpts.FeaturesV4 = &firewallDrivers.FeatureOpts{}
	}

	if n.hasIPv6Firewall() {
		fwOpts.FeaturesV6 = &firewallDrivers.FeatureOpts{}
	}

	if n.config["security.acls"] != "" {
		fwOpts.ACL = true
	}

	// Snapshot container specific IPv4 routes (added with boot proto) before removing IPv4 addresses.
	// This is because the kernel removes any static routes on an interface when all addresses removed.
	ctRoutes, err := n.bootRoutesV4()
	if err != nil {
		return err
	}

	// Flush all IPv4 addresses and routes.
	addr := &ip.Addr{
		DevName: n.name,
		Scope:   "global",
		Family:  ip.FamilyV4,
	}

	err = addr.Flush()
	if err != nil {
		return err
	}

	r := &ip.Route{
		DevName: n.name,
		Proto:   "static",
		Family:  ip.FamilyV4,
	}

	err = r.Flush()
	if err != nil {
		return err
	}

	// Configure IPv4 firewall (includes fan).
	if n.config["bridge.mode"] == "fan" || !slices.Contains([]string{"", "none"}, n.config["ipv4.address"]) {
		if n.hasDHCPv4() && n.hasIPv4Firewall() {
			fwOpts.FeaturesV4.ICMPDHCPDNSAccess = true
		}

		// Allow forwarding.
		if n.config["bridge.mode"] == "fan" || shared.IsTrueOrEmpty(n.config["ipv4.routing"]) {
			err = util.SysctlSet("net/ipv4/ip_forward", "1")
			if err != nil {
				return err
			}

			if n.hasIPv4Firewall() {
				fwOpts.FeaturesV4.ForwardingAllow = true
			}
		}
	}

	// Start building dnsmasq arguments.
	dnsmasqCmd, err := n.getDnsmasqArgs(&bridge)
	if err != nil {
		return err
	}

	var ipv4Address net.IP

	// Configure IPv4.
	if !slices.Contains([]string{"", "none"}, n.config["ipv4.address"]) {
		var subnet *net.IPNet

		// Parse the subnet.
		ipv4Address, subnet, err = net.ParseCIDR(n.config["ipv4.address"])
		if err != nil {
			return fmt.Errorf("Failed parsing ipv4.address: %w", err)
		}

		// Add the address.
		addr := &ip.Addr{
			DevName: n.name,
			Address: n.config["ipv4.address"],
			Family:  ip.FamilyV4,
		}

		err = addr.Add()
		if err != nil {
			return err
		}

		// Configure NAT.
		if shared.IsTrue(n.config["ipv4.nat"]) {
			// If a SNAT source address is specified, use that, otherwise default to MASQUERADE mode.
			var srcIP net.IP
			if n.config["ipv4.nat.address"] != "" {
				srcIP = net.ParseIP(n.config["ipv4.nat.address"])
			}

			fwOpts.SNATV4 = &firewallDrivers.SNATOpts{
				SNATAddress: srcIP,
				Subnet:      subnet,
			}

			if n.config["ipv4.nat.order"] == "after" {
				fwOpts.SNATV4.Append = true
			}
		}

		// Add additional routes.
		if n.config["ipv4.routes"] != "" {
			for route := range strings.SplitSeq(n.config["ipv4.routes"], ",") {
				route = strings.TrimSpace(route)
				r := &ip.Route{
					DevName: n.name,
					Route:   route,
					Proto:   "static",
					Family:  ip.FamilyV4,
				}

				err = r.Add()
				if err != nil {
					return err
				}
			}
		}

		// Restore container specific IPv4 routes to interface.
		n.applyBootRoutesV4(ctRoutes)
	}

	// Snapshot container specific IPv6 routes (added with boot proto) before removing IPv6 addresses.
	// This is because the kernel removes any static routes on an interface when all addresses removed.
	ctRoutes, err = n.bootRoutesV6()
	if err != nil {
		return err
	}

	// Flush all IPv6 addresses and routes.
	addr = &ip.Addr{
		DevName: n.name,
		Scope:   "global",
		Family:  ip.FamilyV6,
	}

	err = addr.Flush()
	if err != nil {
		return err
	}

	r = &ip.Route{
		DevName: n.name,
		Proto:   "static",
		Family:  ip.FamilyV6,
	}

	err = r.Flush()
	if err != nil {
		return err
	}

	var ipv6Address net.IP

	// Configure IPv6.
	if !slices.Contains([]string{"", "none"}, n.config["ipv6.address"]) {
		// Enable IPv6 for the subnet.
		err := util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/disable_ipv6", n.name), "0")
		if err != nil {
			return err
		}

		var subnet *net.IPNet

		// Parse the subnet.
		ipv6Address, subnet, err = net.ParseCIDR(n.config["ipv6.address"])
		if err != nil {
			return fmt.Errorf("Failed parsing ipv6.address: %w", err)
		}

		subnetSize, _ := subnet.Mask.Size()

		if subnetSize < 64 {
			n.logger.Warn("IPv6 networks with a prefix larger than 64 aren't properly supported by dnsmasq")
			err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.UpsertWarningLocalNode(ctx, n.project, entity.TypeNetwork, int(n.id), warningtype.LargerIPv6PrefixThanSupported, "")
			})
			if err != nil {
				n.logger.Warn("Failed to create warning", logger.Ctx{"err": err})
			}
		} else {
			err = warnings.ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(n.state.DB.Cluster, n.project, warningtype.LargerIPv6PrefixThanSupported, entity.TypeNetwork, int(n.id))
			if err != nil {
				n.logger.Warn("Failed to resolve warning", logger.Ctx{"err": err})
			}
		}

		if n.DHCPv6Subnet() != nil {
			if n.hasIPv6Firewall() {
				fwOpts.FeaturesV6.ICMPDHCPDNSAccess = true
			}
		}

		// Allow forwarding.
		if shared.IsTrueOrEmpty(n.config["ipv6.routing"]) {
			// Get a list of proc entries.
			entries, err := os.ReadDir("/proc/sys/net/ipv6/conf/")
			if err != nil {
				return err
			}

			// First set accept_ra to 2 for all interfaces (if not disabled).
			// This ensures that the host can still receive IPv6 router advertisements even with
			// forwarding enabled (which enable below), as the default is to ignore router adverts
			// when forward is enabled, and this could render the host unreachable if it uses
			// SLAAC generated IPs.
			for _, entry := range entries {
				// Check that IPv6 router advertisement acceptance is enabled currently.
				// If its set to 0 then we don't want to enable, and if its already set to 2 then
				// we don't need to do anything.
				content, err := os.ReadFile(fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/accept_ra", entry.Name()))
				if err == nil && string(content) != "1\n" {
					continue
				}

				// If IPv6 router acceptance is enabled (set to 1) then we now set it to 2.
				err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/accept_ra", entry.Name()), "2")
				if err != nil && !os.IsNotExist(err) {
					return err
				}
			}

			// Then set forwarding for all of them.
			for _, entry := range entries {
				err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/forwarding", entry.Name()), "1")
				if err != nil && !os.IsNotExist(err) {
					return err
				}
			}

			if n.hasIPv6Firewall() {
				fwOpts.FeaturesV6.ForwardingAllow = true
			}
		}

		// Add the address.
		addr := &ip.Addr{
			DevName: n.name,
			Address: n.config["ipv6.address"],
			Family:  ip.FamilyV6,
		}

		err = addr.Add()
		if err != nil {
			return err
		}

		// Configure NAT.
		if shared.IsTrue(n.config["ipv6.nat"]) {
			// If a SNAT source address is specified, use that, otherwise default to MASQUERADE mode.
			var srcIP net.IP
			if n.config["ipv6.nat.address"] != "" {
				srcIP = net.ParseIP(n.config["ipv6.nat.address"])
			}

			fwOpts.SNATV6 = &firewallDrivers.SNATOpts{
				SNATAddress: srcIP,
				Subnet:      subnet,
			}

			if n.config["ipv6.nat.order"] == "after" {
				fwOpts.SNATV6.Append = true
			}
		}

		// Add additional routes.
		if n.config["ipv6.routes"] != "" {
			for route := range strings.SplitSeq(n.config["ipv6.routes"], ",") {
				route = strings.TrimSpace(route)
				r := &ip.Route{
					DevName: n.name,
					Route:   route,
					Proto:   "static",
					Family:  ip.FamilyV6,
				}

				err = r.Add()
				if err != nil {
					return err
				}
			}
		}

		// Restore container specific IPv6 routes to interface.
		n.applyBootRoutesV6(ctRoutes)
	}

	// Configure the fan.
	dnsClustered := false
	dnsClusteredAddress := ""
	var overlaySubnet *net.IPNet
	if n.config["bridge.mode"] == "fan" {
		tunName := n.name + "-fan"

		// Parse the underlay.
		underlay := n.config["fan.underlay_subnet"]
		_, underlaySubnet, err := net.ParseCIDR(underlay)
		if err != nil {
			return fmt.Errorf("Failed parsing fan.underlay_subnet: %w", err)
		}

		// Parse the overlay.
		overlay := n.config["fan.overlay_subnet"]
		if overlay == "" {
			overlay = "240.0.0.0/8"
		}

		_, overlaySubnet, err = net.ParseCIDR(overlay)
		if err != nil {
			return fmt.Errorf("Failed parsing fan.overlay_subnet: %w", err)
		}

		// Get the address.
		fanAddress, devName, devAddr, err := n.fanAddress(underlaySubnet, overlaySubnet)
		if err != nil {
			return err
		}

		address, _, _ := strings.Cut(fanAddress, "/")
		if n.config["fan.type"] == "ipip" {
			fanAddress = address + "/24"
		}

		// Update the MTU based on overlay device (if available).
		fanMTU, err := GetDevMTU(devName)
		if err == nil {
			// Apply overhead.
			if n.config["fan.type"] == "ipip" {
				fanMTU = fanMTU - 20
			} else {
				fanMTU = fanMTU - 50
			}

			// Apply changes.
			if fanMTU != bridge.MTU {
				bridge.MTU = fanMTU
				if n.config["bridge.driver"] != "openvswitch" {
					mtuLink := &ip.Link{Name: n.name + "-mtu"}
					err = mtuLink.SetMTU(bridge.MTU)
					if err != nil {
						return err
					}
				}

				err = bridge.SetMTU(bridge.MTU)
				if err != nil {
					return err
				}
			}
		}

		// Add the address.
		ipAddr := &ip.Addr{
			DevName: n.name,
			Address: fanAddress,
			Family:  ip.FamilyV4,
		}

		err = ipAddr.Add()
		if err != nil {
			return err
		}

		// Update the dnsmasq config.
		dnsmasqCmd, err = n.addDnsmasqFanArgs(dnsmasqCmd, address, fanMTU)
		if err != nil {
			return err
		}

		// Save the dnsmasq listen address so that firewall rules can be added later
		ipv4Address = net.ParseIP(address)

		// Setup the tunnel.
		if n.config["fan.type"] == "ipip" {
			r := &ip.Route{
				DevName: "tunl0",
				Family:  ip.FamilyV4,
			}

			err = r.Flush()
			if err != nil {
				return err
			}

			tunLink := &ip.Link{Name: "tunl0"}
			err = tunLink.SetUp()
			if err != nil {
				return err
			}

			// Fails if the map is already set.
			_ = tunLink.Change("ipip", fmt.Sprintf("%s:%s", overlay, underlay))

			r = &ip.Route{
				DevName: "tunl0",
				Route:   overlay,
				Src:     address,
				Proto:   "static",
			}

			err = r.Add()
			if err != nil {
				return err
			}
		} else {
			vxlanID := strconv.FormatUint(uint64(binary.BigEndian.Uint32(overlaySubnet.IP.To4())>>8), 10)
			vxlan := &ip.Vxlan{
				Link:    ip.Link{Name: tunName},
				VxlanID: vxlanID,
				DevName: devName,
				DstPort: "0",
				Local:   devAddr,
				FanMap:  fmt.Sprintf("%s:%s", overlay, underlay),
			}

			err = vxlan.Add()
			if err != nil {
				return err
			}

			err = AttachInterface(n.name, tunName)
			if err != nil {
				return err
			}

			err = vxlan.SetMTU(bridge.MTU)
			if err != nil {
				return err
			}

			err = vxlan.SetUp()
			if err != nil {
				return err
			}

			err = bridge.SetUp()
			if err != nil {
				return err
			}
		}

		// Configure NAT.
		if shared.IsTrue(n.config["ipv4.nat"]) {
			fwOpts.SNATV4 = &firewallDrivers.SNATOpts{
				SNATAddress: nil, // Use MASQUERADE mode.
				Subnet:      overlaySubnet,
			}

			if n.config["ipv4.nat.order"] == "after" {
				fwOpts.SNATV4.Append = true
			}
		}

		// Setup clustered DNS.
		localClusterAddress := n.state.LocalConfig.ClusterAddress()

		// If clusterAddress is non-empty, this indicates the intention for this node to be
		// part of a cluster and so we should ensure that dnsmasq and forkdns are started
		// in cluster mode. Note: During LXD initialisation the cluster may not actually be
		// setup yet, but we want the DNS processes to be ready for when it is.
		if localClusterAddress != "" {
			dnsClustered = true
		}

		dnsClusteredAddress = strings.Split(fanAddress, "/")[0]
	}

	// Configure tunnels.
	for _, tunnel := range tunnels {
		getConfig := func(key string) string {
			return n.config[fmt.Sprintf("tunnel.%s.%s", tunnel, key)]
		}

		tunProtocol := getConfig("protocol")
		tunLocal := getConfig("local")
		tunRemote := getConfig("remote")
		tunName := fmt.Sprintf("%s-%s", n.name, tunnel)

		// Configure the tunnel.
		if tunProtocol == "gre" {
			// Skip partial configs.
			if tunProtocol == "" || tunLocal == "" || tunRemote == "" {
				continue
			}

			gretap := &ip.Gretap{
				Link:   ip.Link{Name: tunName},
				Local:  tunLocal,
				Remote: tunRemote,
			}

			err := gretap.Add()
			if err != nil {
				return err
			}
		} else if tunProtocol == "vxlan" {
			tunGroup := getConfig("group")
			tunInterface := getConfig("interface")

			// Skip partial configs.
			if tunProtocol == "" {
				continue
			}

			vxlan := &ip.Vxlan{
				Link: ip.Link{Name: tunName},
			}

			if tunLocal != "" && tunRemote != "" {
				vxlan.Local = tunLocal
				vxlan.Remote = tunRemote
			} else {
				if tunGroup == "" {
					tunGroup = "239.0.0.1"
				}

				devName := tunInterface
				if devName == "" {
					_, devName, err = DefaultGatewaySubnetV4()
					if err != nil {
						return err
					}
				}

				vxlan.Group = tunGroup
				vxlan.DevName = devName
			}

			tunPort := getConfig("port")
			if tunPort == "" {
				tunPort = "0"
			}

			vxlan.DstPort = tunPort

			tunID := getConfig("id")
			if tunID == "" {
				tunID = "1"
			}

			vxlan.VxlanID = tunID

			tunTTL := getConfig("ttl")
			if tunTTL == "" {
				tunTTL = "1"
			}

			vxlan.TTL = tunTTL

			err := vxlan.Add()
			if err != nil {
				return err
			}
		}

		// Bridge it and bring up.
		err = AttachInterface(n.name, tunName)
		if err != nil {
			return err
		}

		tunLink := &ip.Link{Name: tunName}
		err = tunLink.SetMTU(bridge.MTU)
		if err != nil {
			return err
		}

		// Bring up tunnel interface.
		err = tunLink.SetUp()
		if err != nil {
			return err
		}

		// Bring up network interface.
		err = bridge.SetUp()
		if err != nil {
			return err
		}
	}

	// Generate and load apparmor profiles.
	err = apparmor.NetworkLoad(n.state.OS, n)
	if err != nil {
		return err
	}

	// Kill any existing dnsmasq and forkdns daemon for this network.
	err = dnsmasq.Kill(n.name, false)
	if err != nil {
		return err
	}

	err = n.killForkDNS()
	if err != nil {
		return err
	}

	// Configure dnsmasq.
	if n.UsesDNSMasq() {
		err = n.startDnsmasq(dnsmasqCmd, dnsClustered, dnsClusteredAddress, overlaySubnet)
		if err != nil {
			return err
		}

		// Spawn DNS forwarder if needed (backgrounded to avoid deadlocks during cluster boot).
		if dnsClustered {
			// Create forkdns servers directory.
			forkdnsPath := shared.VarPath("networks", n.name, ForkdnsServersListPath)
			if !shared.PathExists(forkdnsPath) {
				err = os.MkdirAll(forkdnsPath, 0755)
				if err != nil {
					return err
				}
			}

			// Create forkdns servers.conf file if doesn't exist.
			f, err := os.OpenFile(forkdnsPath+"/"+ForkdnsServersListFile, os.O_RDONLY|os.O_CREATE, 0666)
			if err != nil {
				return err
			}

			_ = f.Close()

			err = n.spawnForkDNS(dnsClusteredAddress)
			if err != nil {
				return err
			}
		}
	} else {
		// Clean up old dnsmasq config if exists and we are not starting dnsmasq.
		leasesPath := shared.VarPath("networks", n.name, "dnsmasq.leases")
		if shared.PathExists(leasesPath) {
			err := os.Remove(leasesPath)
			if err != nil {
				return fmt.Errorf("Failed to remove old dnsmasq leases file %q: %w", leasesPath, err)
			}
		}

		// Clean up old dnsmasq PID file.
		pidPath := shared.VarPath("networks", n.name, "dnsmasq.pid")
		if shared.PathExists(pidPath) {
			err := os.Remove(pidPath)
			if err != nil {
				return fmt.Errorf("Failed to remove old dnsmasq pid file %q: %w", pidPath, err)
			}
		}
	}

	// Setup firewall.
	n.logger.Debug("Setting up firewall")
	err = n.state.Firewall.NetworkSetup(n.name, ipv4Address, ipv6Address, fwOpts)
	if err != nil {
		return fmt.Errorf("Failed to setup firewall: %w", err)
	}

	if fwOpts.ACL {
		aclNet := acl.NetworkACLUsage{
			Name:   n.Name(),
			Type:   n.Type(),
			ID:     n.ID(),
			Config: n.Config(),
		}

		n.logger.Debug("Applying up firewall ACLs")
		err = acl.FirewallApplyACLRules(n.state, n.logger, n.Project(), aclNet)
		if err != nil {
			return err
		}
	}

	// Setup network address forwards.
	err = n.forwardSetupFirewall()
	if err != nil {
		return err
	}

	nodeEvacuated := n.state.DB.Cluster.LocalNodeIsEvacuated()

	// Setup BGP.
	if !nodeEvacuated {
		err = n.bgpSetup(oldConfig)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// Stop stops the network.
func (n *bridge) Stop() error {
	n.logger.Debug("Stop")

	if !n.isRunning() {
		return nil
	}

	// Clear BGP.
	err := n.bgpClear(n.config)
	if err != nil {
		return err
	}

	// Kill any existing dnsmasq and forkdns daemon for this network
	err = dnsmasq.Kill(n.name, false)
	if err != nil {
		return err
	}

	err = n.killForkDNS()
	if err != nil {
		return err
	}

	// Destroy the bridge interface
	if n.config["bridge.driver"] == "openvswitch" {
		ovs := openvswitch.NewOVS()
		err := ovs.BridgeDelete(n.name)
		if err != nil {
			return err
		}
	} else {
		bridgeLink := &ip.Link{Name: n.name}
		err := bridgeLink.Delete()
		if err != nil {
			return err
		}
	}

	// Fully clear firewall setup.
	fwClearIPVersions := []uint{}

	if usesIPv4Firewall(n.config) {
		fwClearIPVersions = append(fwClearIPVersions, 4)
	}

	if usesIPv6Firewall(n.config) {
		fwClearIPVersions = append(fwClearIPVersions, 6)
	}

	if len(fwClearIPVersions) > 0 {
		n.logger.Debug("Deleting firewall")
		err := n.state.Firewall.NetworkClear(n.name, true, fwClearIPVersions)
		if err != nil {
			return fmt.Errorf("Failed deleting firewall: %w", err)
		}
	}

	// Get a list of interfaces
	ifaces, err := net.Interfaces()
	if err != nil {
		return err
	}

	// Cleanup any existing tunnel device
	for _, iface := range ifaces {
		if strings.HasPrefix(iface.Name, n.name+"-") {
			tunLink := &ip.Link{Name: iface.Name}
			err = tunLink.Delete()
			if err != nil {
				return err
			}
		}
	}

	// Unload apparmor profiles.
	err = apparmor.NetworkUnload(n.state.OS, n)
	if err != nil {
		return err
	}

	return nil
}

// Evacuate the network by clearing BGP.
func (n *bridge) Evacuate() error {
	n.logger.Debug("Evacuate")

	// Clear BGP.
	return n.bgpClear(n.config)
}

// Restore the network by setting up BGP.
func (n *bridge) Restore() error {
	n.logger.Debug("Restore")

	// Setup BGP.
	return n.bgpSetup(nil)
}

// Update updates the network. Accepts notification boolean indicating if this update request is coming from a
// cluster notification, in which case do not update the database, just apply local changes needed.
func (n *bridge) Update(newNetwork api.NetworkPut, targetNode string, clientType request.ClientType) error {
	n.logger.Debug("Update", logger.Ctx{"clientType": clientType, "newNetwork": newNetwork})

	err := n.populateAutoConfig(newNetwork.Config)
	if err != nil {
		return fmt.Errorf("Failed generating auto config: %w", err)
	}

	dbUpdateNeeded, changedKeys, oldNetwork, err := n.configChanged(newNetwork)
	if err != nil {
		return err
	}

	if !dbUpdateNeeded {
		return nil // Nothing changed.
	}

	// If the network as a whole has not had any previous creation attempts, or the node itself is still
	// pending, then don't apply the new settings to the node, just to the database record (ready for the
	// actual global create request to be initiated).
	if n.Status() == api.NetworkStatusPending || n.LocalStatus() == api.NetworkStatusPending {
		return n.update(newNetwork, targetNode, clientType)
	}

	revert := revert.New()
	defer revert.Fail()

	// Perform any pre-update cleanup needed if local member network was already created.
	if len(changedKeys) > 0 {
		// Define a function which reverts everything.
		revert.Add(func() {
			// Reset changes to all nodes and database.
			_ = n.update(oldNetwork, targetNode, clientType)

			// Reset any change that was made to local bridge.
			_ = n.setup(newNetwork.Config)
		})

		// Bring the bridge down entirely if the driver has changed.
		if slices.Contains(changedKeys, "bridge.driver") && n.isRunning() {
			err = n.Stop()
			if err != nil {
				return err
			}
		}

		// Detach any external interfaces should no longer be attached.
		if slices.Contains(changedKeys, "bridge.external_interfaces") && n.isRunning() {
			devices := []string{}
			for dev := range strings.SplitSeq(newNetwork.Config["bridge.external_interfaces"], ",") {
				dev = strings.TrimSpace(dev)
				devices = append(devices, dev)
			}

			for dev := range strings.SplitSeq(oldNetwork.Config["bridge.external_interfaces"], ",") {
				dev = strings.TrimSpace(dev)
				if dev == "" {
					continue
				}

				if !slices.Contains(devices, dev) && InterfaceExists(dev) {
					err = DetachInterface(n.name, dev)
					if err != nil {
						return err
					}
				}
			}
		}
	}

	// Apply changes to all nodes and database.
	err = n.update(newNetwork, targetNode, clientType)
	if err != nil {
		return err
	}

	// Restart the network if needed.
	if len(changedKeys) > 0 {
		err = n.setup(oldNetwork.Config)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

func (n *bridge) spawnForkDNS(listenAddress string) error {
	// Reject known bad names that might cause problem when dealing with paths.
	err := n.ValidateName(n.Name())
	if err != nil {
		return fmt.Errorf("Invalid network name: %q: %v", n.Name(), err)
	}

	// Setup the dnsmasq domain
	dnsDomain := n.config["dns.domain"]
	if dnsDomain == "" {
		dnsDomain = "lxd"
	}

	// Spawn the daemon using subprocess
	command := n.state.OS.ExecPath
	forkdnsargs := []string{"forkdns",
		listenAddress + ":1053",
		dnsDomain,
		n.name}

	logPath := shared.LogPath("forkdns." + n.name + ".log")

	p, err := subprocess.NewProcess(command, forkdnsargs, logPath, logPath)
	if err != nil {
		return fmt.Errorf("Failed to create subprocess: %s", err)
	}

	// Drop privileges.
	p.SetCreds(n.state.OS.UnprivUID, n.state.OS.UnprivGID)

	// Apply AppArmor profile.
	p.SetApparmor(apparmor.ForkdnsProfileName(n))

	err = p.Start(context.Background())
	if err != nil {
		return fmt.Errorf("Failed to run: %s %s: %w", command, strings.Join(forkdnsargs, " "), err)
	}

	err = p.Save(shared.VarPath("networks", n.name, "forkdns.pid"))
	if err != nil {
		// Kill Process if started, but could not save the file
		err2 := p.Stop()
		if err2 != nil {
			return fmt.Errorf("Could not kill subprocess while handling saving error: %s: %s", err, err2)
		}

		return fmt.Errorf("Failed to save subprocess details: %s", err)
	}

	return nil
}

// HandleHeartbeat refreshes forkdns servers. Retrieves the IPv4 address of each cluster node (excluding ourselves)
// for this network. It then updates the forkdns server list file if there are changes.
func (n *bridge) HandleHeartbeat(heartbeatData *cluster.APIHeartbeat) error {
	// Make sure forkdns has been setup.
	if !shared.PathExists(shared.VarPath("networks", n.name, "forkdns.pid")) {
		return nil
	}

	addresses := []string{}
	localClusterAddress := n.state.LocalConfig.ClusterAddress()

	n.logger.Info("Refreshing forkdns peers")

	networkCert := n.state.Endpoints.NetworkCert()
	for _, node := range heartbeatData.Members {
		if node.Address == localClusterAddress {
			// No need to query ourselves.
			continue
		}

		if !node.Online {
			n.logger.Warn("Excluding offline member from DNS peers refresh", logger.Ctx{"address": node.Address, "ID": node.ID, "raftID": node.RaftID, "lastHeartbeat": node.LastHeartbeat})
			continue
		}

		client, err := cluster.Connect(context.Background(), node.Address, networkCert, n.state.ServerCert(), true)
		if err != nil {
			return err
		}

		state, err := client.GetNetworkState(n.name)
		if err != nil {
			return err
		}

		for _, addr := range state.Addresses {
			// Only get IPv4 addresses of nodes on network.
			if addr.Family != "inet" || addr.Scope != "global" {
				continue
			}

			addresses = append(addresses, addr.Address)
			break
		}
	}

	// Compare current stored list to retrieved list and see if we need to update.
	curList, err := ForkdnsServersList(n.name)
	if err != nil {
		// Only warn here, but continue on to regenerate the servers list from cluster info.
		n.logger.Warn("Failed to load existing forkdns server list", logger.Ctx{"err": err})
	}

	// If current list is same as cluster list, nothing to do.
	if err == nil && reflect.DeepEqual(curList, addresses) {
		return nil
	}

	err = n.updateForkdnsServersFile(addresses)
	if err != nil {
		return err
	}

	n.logger.Info("Updated forkdns server list", logger.Ctx{"nodes": addresses})
	return nil
}

func (n *bridge) getTunnels() []string {
	tunnels := []string{}

	for k := range n.config {
		if !strings.HasPrefix(k, "tunnel.") {
			continue
		}

		fields := strings.Split(k, ".")
		if !slices.Contains(tunnels, fields[1]) {
			tunnels = append(tunnels, fields[1])
		}
	}

	return tunnels
}

// bootRoutesV4 returns a list of IPv4 boot routes on the network's device.
func (n *bridge) bootRoutesV4() ([]string, error) {
	r := &ip.Route{
		DevName: n.name,
		Proto:   "boot",
		Family:  ip.FamilyV4,
	}

	routes, err := r.Show()
	if err != nil {
		return nil, err
	}

	return routes, nil
}

// bootRoutesV6 returns a list of IPv6 boot routes on the network's device.
func (n *bridge) bootRoutesV6() ([]string, error) {
	r := &ip.Route{
		DevName: n.name,
		Proto:   "boot",
		Family:  ip.FamilyV6,
	}

	routes, err := r.Show()
	if err != nil {
		return nil, err
	}

	return routes, nil
}

// applyBootRoutesV4 applies a list of IPv4 boot routes to the network's device.
func (n *bridge) applyBootRoutesV4(routes []string) {
	for _, route := range routes {
		r := &ip.Route{
			DevName: n.name,
			Proto:   "boot",
			Family:  ip.FamilyV4,
		}

		err := r.Replace(strings.Fields(route))
		if err != nil {
			// If it fails, then we can't stop as the route has already gone, so just log and continue.
			n.logger.Error("Failed to restore route", logger.Ctx{"err": err})
		}
	}
}

// applyBootRoutesV6 applies a list of IPv6 boot routes to the network's device.
func (n *bridge) applyBootRoutesV6(routes []string) {
	for _, route := range routes {
		r := &ip.Route{
			DevName: n.name,
			Proto:   "boot",
			Family:  ip.FamilyV6,
		}

		err := r.Replace(strings.Fields(route))
		if err != nil {
			// If it fails, then we can't stop as the route has already gone, so just log and continue.
			n.logger.Error("Failed to restore route", logger.Ctx{"err": err})
		}
	}
}

func (n *bridge) fanAddress(underlay *net.IPNet, overlay *net.IPNet) (cidr string, dev string, ipStr string, err error) {
	// Quick checks.
	underlaySize, _ := underlay.Mask.Size()
	if underlaySize != 16 && underlaySize != 24 {
		return "", "", "", errors.New("Only /16 or /24 underlays are supported at this time")
	}

	overlaySize, _ := overlay.Mask.Size()
	if overlaySize != 8 && overlaySize != 16 {
		return "", "", "", errors.New("Only /8 or /16 overlays are supported at this time")
	}

	if overlaySize+(32-underlaySize)+8 > 32 {
		return "", "", "", errors.New("Underlay or overlay networks too large to accommodate the FAN")
	}

	// Get the IP
	ip, dev, err := n.addressForSubnet(underlay)
	if err != nil {
		return "", "", "", err
	}

	ipStr = ip.String()

	// Force into IPv4 format
	ipBytes := ip.To4()
	if ipBytes == nil {
		return "", "", "", fmt.Errorf("Invalid IPv4: %s", ip)
	}

	// Compute the IP
	ipBytes[0] = overlay.IP[0]
	if overlaySize == 16 {
		ipBytes[1] = overlay.IP[1]
		ipBytes[2] = ipBytes[3]
	} else if underlaySize == 24 {
		ipBytes[1] = ipBytes[3]
		ipBytes[2] = 0
	} else if underlaySize == 16 {
		ipBytes[1] = ipBytes[2]
		ipBytes[2] = ipBytes[3]
	}

	ipBytes[3] = 1

	return ipBytes.String() + "/" + strconv.Itoa(overlaySize), dev, ipStr, err
}

func (n *bridge) addressForSubnet(subnet *net.IPNet) (net.IP, string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return net.IP{}, "", err
	}

	for _, iface := range ifaces {
		// Skip addresses on lo interface in case VIPs are being used on that interface that are part of
		// the underlay subnet as is unlikely to be the actual intended underlay subnet interface.
		if iface.Name == "lo" {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}

			if subnet.Contains(ip) {
				return ip, iface.Name, nil
			}
		}
	}

	return net.IP{}, "", errors.New("No address found in subnet")
}

func (n *bridge) killForkDNS() error {
	// Check if we have a running forkdns at all
	pidPath := shared.VarPath("networks", n.name, "forkdns.pid")

	// If the pid file doesn't exist, there is no process to kill.
	if !shared.PathExists(pidPath) {
		return nil
	}

	p, err := subprocess.ImportProcess(pidPath)
	if err != nil {
		return fmt.Errorf("Could not read pid file: %s", err)
	}

	err = p.Stop()
	if err != nil && err != subprocess.ErrNotRunning {
		return fmt.Errorf("Unable to kill dnsmasq: %s", err)
	}

	return nil
}

// updateForkdnsServersFile takes a list of node addresses and writes them atomically to
// the forkdns.servers file ready for forkdns to notice and re-apply its config.
func (n *bridge) updateForkdnsServersFile(addresses []string) error {
	// We don't want to race with ourselves here
	forkdnsServersLock.Lock()
	defer forkdnsServersLock.Unlock()

	permName := shared.VarPath("networks", n.name, ForkdnsServersListPath+"/"+ForkdnsServersListFile)
	tmpName := permName + ".tmp"

	// Open tmp file and truncate
	tmpFile, err := os.Create(tmpName)
	if err != nil {
		return err
	}

	defer func() { _ = tmpFile.Close() }()

	for _, address := range addresses {
		_, err := tmpFile.WriteString(address + "\n")
		if err != nil {
			return err
		}
	}

	err = tmpFile.Close()
	if err != nil {
		return err
	}

	// Atomically rename finished file into permanent location so forkdns can pick it up.
	err = os.Rename(tmpName, permName)
	if err != nil {
		return err
	}

	return nil
}

// hasIPv4Firewall indicates whether the network has IPv4 firewall enabled.
func (n *bridge) hasIPv4Firewall() bool {
	// IPv4 firewall is only enabled if there is a bridge ipv4.address or fan mode, and ipv4.firewall enabled.
	// When using fan bridge.mode, there can be an empty ipv4.address, so we assume it is active.
	if (n.config["bridge.mode"] == "fan" || !slices.Contains([]string{"", "none"}, n.config["ipv4.address"])) && shared.IsTrueOrEmpty(n.config["ipv4.firewall"]) {
		return true
	}

	return false
}

// hasIPv6Firewall indicates whether the network has IPv6 firewall enabled.
func (n *bridge) hasIPv6Firewall() bool {
	// IPv6 firewall is only enabled if there is a bridge ipv6.address and ipv6.firewall enabled.
	if !slices.Contains([]string{"", "none"}, n.config["ipv6.address"]) && shared.IsTrueOrEmpty(n.config["ipv6.firewall"]) {
		return true
	}

	return false
}

// hasDHCPv4 indicates whether the network has DHCPv4 enabled.
// An empty ipv4.dhcp setting indicates enabled by default.
func (n *bridge) hasDHCPv4() bool {
	return shared.IsTrueOrEmpty(n.config["ipv4.dhcp"])
}

// hasDHCPv6 indicates whether the network has DHCPv6 enabled.
// An empty ipv6.dhcp setting indicates enabled by default.
func (n *bridge) hasDHCPv6() bool {
	return shared.IsTrueOrEmpty(n.config["ipv6.dhcp"])
}

// DHCPv4Subnet returns the DHCPv4 subnet (if DHCP is enabled on network).
func (n *bridge) DHCPv4Subnet() *net.IPNet {
	// DHCP is disabled on this network.
	if !n.hasDHCPv4() {
		return nil
	}

	// Fan mode. Extract DHCP subnet from fan bridge address. Only detectable once network has started.
	// But if there is no address on the fan bridge then DHCP won't work anyway.
	if n.config["bridge.mode"] == "fan" {
		iface, err := net.InterfaceByName(n.name)
		if err != nil {
			return nil
		}

		addrs, err := iface.Addrs()
		if err != nil {
			return nil
		}

		for _, addr := range addrs {
			ip, subnet, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}

			if ip != nil && ip.To4() != nil && ip.IsGlobalUnicast() {
				return subnet // Use first IPv4 unicast address on host for DHCP subnet.
			}
		}

		return nil // No addresses found, means DHCP must be disabled.
	}

	// Non-fan mode. Return configured bridge subnet directly.
	_, subnet, err := net.ParseCIDR(n.config["ipv4.address"])
	if err != nil {
		return nil
	}

	return subnet
}

// DHCPv6Subnet returns the DHCPv6 subnet (if DHCP or SLAAC is enabled on network).
func (n *bridge) DHCPv6Subnet() *net.IPNet {
	// DHCP is disabled on this network.
	if !n.hasDHCPv6() {
		return nil
	}

	_, subnet, err := net.ParseCIDR(n.config["ipv6.address"])
	if err != nil {
		return nil
	}

	return subnet
}

// forwardConvertToFirewallForward converts forwards into format compatible with the firewall package.
func (n *bridge) forwardConvertToFirewallForwards(listenAddress net.IP, defaultTargetAddress net.IP, portMaps []*forwardPortMap) []firewallDrivers.AddressForward {
	vips := make([]firewallDrivers.AddressForward, 0, len(portMaps)+1)

	if defaultTargetAddress != nil {
		vips = append(vips, firewallDrivers.AddressForward{
			ListenAddress: listenAddress,
			TargetAddress: defaultTargetAddress,
		})
	}

	for _, portMap := range portMaps {
		vips = append(vips, firewallDrivers.AddressForward{
			ListenAddress: listenAddress,
			Protocol:      portMap.protocol,
			TargetAddress: portMap.target.address,
			ListenPorts:   portMap.listenPorts,
			TargetPorts:   portMap.target.ports,
		})
	}

	return vips
}

// bridgeProjectNetworks takes a map of all networks in all projects and returns a filtered map of bridge networks.
func (n *bridge) bridgeProjectNetworks(projectNetworks map[string]map[int64]api.Network) map[string][]*api.Network {
	bridgeProjectNetworks := make(map[string][]*api.Network)
	for netProject, networks := range projectNetworks {
		for _, ni := range networks {
			network := ni // Local var creating pointer to rather than iterator.

			// Skip non-bridge networks.
			if network.Type != "bridge" {
				continue
			}

			if bridgeProjectNetworks[netProject] == nil {
				bridgeProjectNetworks[netProject] = []*api.Network{&network}
			} else {
				bridgeProjectNetworks[netProject] = append(bridgeProjectNetworks[netProject], &network)
			}
		}
	}

	return bridgeProjectNetworks
}

// bridgeNetworkExternalSubnets returns a list of external subnets used by bridge networks. Networks are considered
// to be using external subnets for their ipv4.address and/or ipv6.address if they have NAT disabled, and/or if
// they have external NAT addresses specified.
func (n *bridge) bridgeNetworkExternalSubnets(bridgeProjectNetworks map[string][]*api.Network) ([]externalSubnetUsage, error) {
	externalSubnets := make([]externalSubnetUsage, 0)
	for netProject, networks := range bridgeProjectNetworks {
		for _, netInfo := range networks {
			for _, keyPrefix := range []string{"ipv4", "ipv6"} {
				// If NAT is disabled, then network subnet is an external subnet.
				if shared.IsFalseOrEmpty(netInfo.Config[keyPrefix+".nat"]) {
					key := keyPrefix + ".address"

					_, ipNet, err := net.ParseCIDR(netInfo.Config[key])
					if err != nil {
						continue // Skip invalid/unspecified network addresses.
					}

					externalSubnets = append(externalSubnets, externalSubnetUsage{
						subnet:         *ipNet,
						networkProject: netProject,
						networkName:    netInfo.Name,
						usageType:      subnetUsageNetwork,
					})
				}

				// Find any external subnets used for network SNAT.
				if netInfo.Config[keyPrefix+".nat.address"] != "" {
					key := keyPrefix + ".nat.address"

					subnetSize := 128
					if keyPrefix == "ipv4" {
						subnetSize = 32
					}

					_, ipNet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", netInfo.Config[key], subnetSize))
					if err != nil {
						return nil, fmt.Errorf("Failed parsing %q of %q in project %q: %w", key, netInfo.Name, netProject, err)
					}

					externalSubnets = append(externalSubnets, externalSubnetUsage{
						subnet:         *ipNet,
						networkProject: netProject,
						networkName:    netInfo.Name,
						usageType:      subnetUsageNetworkSNAT,
					})
				}

				// Find any routes being used by the network.
				for _, cidr := range shared.SplitNTrimSpace(netInfo.Config[keyPrefix+".routes"], ",", -1, true) {
					_, ipNet, err := net.ParseCIDR(cidr)
					if err != nil {
						continue // Skip invalid/unspecified network addresses.
					}

					externalSubnets = append(externalSubnets, externalSubnetUsage{
						subnet:         *ipNet,
						networkProject: netProject,
						networkName:    netInfo.Name,
						usageType:      subnetUsageNetwork,
					})
				}
			}
		}
	}

	return externalSubnets, nil
}

// bridgedNICExternalRoutes returns a list of external routes currently used by bridged NICs that are connected to
// networks specified.
func (n *bridge) bridgedNICExternalRoutes(bridgeProjectNetworks map[string][]*api.Network) ([]externalSubnetUsage, error) {
	externalRoutes := make([]externalSubnetUsage, 0)

	err := n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.InstanceList(ctx, func(inst db.InstanceArgs, p api.Project) error {
			// Get the instance's effective network project name.
			instNetworkProject := project.NetworkProjectFromRecord(&p)

			if instNetworkProject != api.ProjectDefaultName {
				return nil // Managed bridge networks can only exist in default project.
			}

			devices := instancetype.ExpandInstanceDevices(inst.Devices, inst.Profiles)

			// Iterate through each of the instance's devices, looking for bridged NICs that are linked to
			// networks specified.
			for devName, devConfig := range devices {
				if devConfig["type"] != "nic" {
					continue
				}

				// Check whether the NIC device references one of the networks supplied.
				if !NICUsesNetwork(devConfig, bridgeProjectNetworks[instNetworkProject]...) {
					continue
				}

				// For bridged NICs that are connected to networks specified, check if they have any
				// routes or external routes configured, and if so add them to the list to return.
				for _, key := range []string{"ipv4.routes", "ipv6.routes", "ipv4.routes.external", "ipv6.routes.external"} {
					for _, cidr := range shared.SplitNTrimSpace(devConfig[key], ",", -1, true) {
						_, ipNet, _ := net.ParseCIDR(cidr)
						if ipNet == nil {
							// Skip if NIC device doesn't have a valid route.
							continue
						}

						externalRoutes = append(externalRoutes, externalSubnetUsage{
							subnet:          *ipNet,
							networkProject:  instNetworkProject,
							networkName:     devConfig["network"],
							instanceProject: inst.Project,
							instanceName:    inst.Name,
							instanceDevice:  devName,
							usageType:       subnetUsageInstance,
						})
					}
				}
			}

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return externalRoutes, nil
}

// getExternalSubnetInUse returns information about usage of external subnets by bridge networks (and NICs
// connected to them) on this member.
func (n *bridge) getExternalSubnetInUse() ([]externalSubnetUsage, error) {
	var err error
	var projectNetworks map[string]map[int64]api.Network
	var projectNetworksForwardsOnUplink map[string]map[int64][]string
	var externalSubnets []externalSubnetUsage

	err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get all managed networks across all projects.
		projectNetworks, err = tx.GetCreatedNetworks(ctx)
		if err != nil {
			return fmt.Errorf("Failed to load all networks: %w", err)
		}

		// Get all network forward listen addresses for forwards assigned to this specific cluster member.
		projectNetworksForwardsOnUplink, err = tx.GetProjectNetworkForwardListenAddressesOnMember(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading network forward listen addresses: %w", err)
		}

		externalSubnets, err = n.common.getExternalSubnetInUse(ctx, tx, n.name, true)
		if err != nil {
			return fmt.Errorf("Failed getting external subnets in use: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Get managed bridge networks.
	bridgeProjectNetworks := n.bridgeProjectNetworks(projectNetworks)

	// Get external subnets used by other managed bridge networks.
	bridgeNetworkExternalSubnets, err := n.bridgeNetworkExternalSubnets(bridgeProjectNetworks)
	if err != nil {
		return nil, err
	}

	// Get external routes configured on bridged NICs.
	bridgedNICExternalRoutes, err := n.bridgedNICExternalRoutes(bridgeProjectNetworks)
	if err != nil {
		return nil, err
	}

	externalSubnets = append(externalSubnets, bridgeNetworkExternalSubnets...)
	externalSubnets = append(externalSubnets, bridgedNICExternalRoutes...)

	err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Detect if there are any conflicting proxy devices on all instances with the to be created network forward
		return tx.InstanceList(ctx, func(inst db.InstanceArgs, p api.Project) error {
			devices := instancetype.ExpandInstanceDevices(inst.Devices, inst.Profiles)

			for devName, devConfig := range devices {
				if devConfig["type"] != "proxy" {
					continue
				}

				proxyListenAddr, err := ProxyParseAddr(devConfig["listen"])
				if err != nil {
					return err
				}

				proxySubnet, err := ParseIPToNet(proxyListenAddr.Address)
				if err != nil {
					continue // If proxy listen isn't a valid IP it can't conflict.
				}

				externalSubnets = append(externalSubnets, externalSubnetUsage{
					usageType:       subnetUsageProxy,
					subnet:          *proxySubnet,
					instanceProject: inst.Project,
					instanceName:    inst.Name,
					instanceDevice:  devName,
				})
			}

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	// Add forward listen addresses to this list.
	for projectName, networks := range projectNetworksForwardsOnUplink {
		for networkID, listenAddresses := range networks {
			for _, listenAddress := range listenAddresses {
				// Convert listen address to subnet.
				listenAddressNet, err := ParseIPToNet(listenAddress)
				if err != nil {
					return nil, fmt.Errorf("Invalid existing forward listen address %q", listenAddress)
				}

				// Create an externalSubnetUsage for the listen address by using the network ID
				// of the listen address to retrieve the already loaded network name from the
				// projectNetworks map.
				externalSubnets = append(externalSubnets, externalSubnetUsage{
					subnet:         *listenAddressNet,
					networkProject: projectName,
					networkName:    projectNetworks[projectName][networkID].Name,
					usageType:      subnetUsageNetworkForward,
				})
			}
		}
	}

	return externalSubnets, nil
}

// forwardValidate validates the forward request.
func (n *bridge) forwardValidate(listenAddress net.IP, forward api.NetworkForwardPut) ([]*forwardPortMap, error) {
	err := n.checkAddressNotInOVNRange(listenAddress)
	if err != nil {
		return nil, err
	}

	return n.common.forwardValidate(listenAddress, forward)
}

// ForwardCreate creates a network forward.
func (n *bridge) ForwardCreate(forward api.NetworkForwardsPost, clientType request.ClientType) (net.IP, error) {
	memberSpecific := true // bridge supports per-member forwards.

	// Convert listen address to subnet so we can check its valid and can be used.
	listenAddressNet, err := ParseIPToNet(forward.ListenAddress)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing address forward listen address %q: %w", forward.ListenAddress, err)
	}

	if listenAddressNet.IP.IsUnspecified() {
		return nil, api.StatusErrorf(http.StatusNotImplemented, "Automatic listen address allocation not supported for drivers of type %q", n.netType)
	}

	err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check if there is an existing forward using the same listen address.
		_, _, err := tx.GetNetworkForward(ctx, n.ID(), memberSpecific, forward.ListenAddress)

		return err
	})
	if err == nil {
		return nil, api.StatusErrorf(http.StatusConflict, "A forward for that listen address already exists")
	}

	_, err = n.forwardValidate(listenAddressNet.IP, forward.NetworkForwardPut)
	if err != nil {
		return nil, err
	}

	externalSubnetsInUse, err := n.getExternalSubnetInUse()
	if err != nil {
		return nil, err
	}

	checkAddressNotInUse := func(netip *net.IPNet) (bool, error) {
		// Check the listen address subnet doesn't fall within any existing network external subnets.
		for _, externalSubnetUser := range externalSubnetsInUse {
			// Check if usage is from our own network.
			if externalSubnetUser.networkProject == n.project && externalSubnetUser.networkName == n.name {
				// Skip checking conflict with our own network's subnet or SNAT address.
				// But do not allow other conflict with other usage types within our own network.
				if externalSubnetUser.usageType == subnetUsageNetwork || externalSubnetUser.usageType == subnetUsageNetworkSNAT {
					continue
				}
			}

			if SubnetContains(&externalSubnetUser.subnet, netip) || SubnetContains(netip, &externalSubnetUser.subnet) {
				return false, nil
			}
		}

		return true, nil
	}

	isValid, err := checkAddressNotInUse(listenAddressNet)
	if err != nil {
		return nil, err
	} else if !isValid {
		// This error is purposefully vague so that it doesn't reveal any names of
		// resources potentially outside of the network.
		return nil, fmt.Errorf("Forward listen address %q overlaps with another network or NIC", listenAddressNet.String())
	}

	revert := revert.New()
	defer revert.Fail()

	var forwardID int64

	err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Create forward DB record.
		forwardID, err = tx.CreateNetworkForward(ctx, n.ID(), memberSpecific, &forward)

		return err
	})
	if err != nil {
		return nil, err
	}

	revert.Add(func() {
		_ = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.DeleteNetworkForward(ctx, n.ID(), forwardID)
		})
		_ = n.forwardSetupFirewall()
		_ = n.forwardBGPSetupPrefixes()
	})

	err = n.forwardSetupFirewall()
	if err != nil {
		return nil, err
	}

	// Check if hairpin mode needs to be enabled on active NIC bridge ports.
	if n.config["bridge.driver"] != "openvswitch" {
		brNetfilterEnabled := false
		for _, ipVersion := range []uint{4, 6} {
			if BridgeNetfilterEnabled(ipVersion) == nil {
				brNetfilterEnabled = true
				break
			}
		}

		// If br_netfilter is enabled and bridge has forwards, we enable hairpin mode on each NIC's bridge
		// port in case any of the forwards target the NIC and the instance attempts to connect to the
		// forward's listener. Without hairpin mode on the target of the forward will not be able to
		// connect to the listener.
		if brNetfilterEnabled {
			var listenAddresses map[int64]string

			err := n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				listenAddresses, err = tx.GetNetworkForwardListenAddresses(ctx, n.ID(), true)

				return err
			})
			if err != nil {
				return nil, fmt.Errorf("Failed loading network forwards: %w", err)
			}

			// If we are the first forward on this bridge, enable hairpin mode on active NIC ports.
			if len(listenAddresses) <= 1 {
				filter := dbCluster.InstanceFilter{Node: &n.state.ServerName}

				err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.InstanceList(ctx, func(inst db.InstanceArgs, p api.Project) error {
						// Get the instance's effective network project name.
						instNetworkProject := project.NetworkProjectFromRecord(&p)

						if instNetworkProject != api.ProjectDefaultName {
							return nil // Managed bridge networks can only exist in default project.
						}

						devices := instancetype.ExpandInstanceDevices(inst.Devices.Clone(), inst.Profiles)

						// Iterate through each of the instance's devices, looking for bridged NICs
						// that are linked to this network.
						for devName, devConfig := range devices {
							if devConfig["type"] != "nic" {
								continue
							}

							// Check whether the NIC device references our network..
							if !NICUsesNetwork(devConfig, &api.Network{Name: n.Name()}) {
								continue
							}

							hostName := inst.Config[fmt.Sprintf("volatile.%s.host_name", devName)]
							if InterfaceExists(hostName) {
								link := &ip.Link{Name: hostName}
								err = link.BridgeLinkSetHairpin(true)
								if err != nil {
									return fmt.Errorf("Error enabling hairpin mode on bridge port %q: %w", link.Name, err)
								}

								n.logger.Debug("Enabled hairpin mode on NIC bridge port", logger.Ctx{"inst": inst.Name, "project": inst.Project, "device": devName, "dev": link.Name})
							}
						}

						return nil
					}, filter)
				})
				if err != nil {
					return nil, err
				}
			}
		}
	}

	// Refresh exported BGP prefixes on local member.
	err = n.forwardBGPSetupPrefixes()
	if err != nil {
		return nil, fmt.Errorf("Failed applying BGP prefixes for address forwards: %w", err)
	}

	revert.Success()
	return listenAddressNet.IP, nil
}

// ForwardUpdate updates a network forward.
func (n *bridge) ForwardUpdate(listenAddress string, req api.NetworkForwardPut, clientType request.ClientType) error {
	memberSpecific := true // bridge supports per-member forwards.

	var curForwardID int64
	var curForward *api.NetworkForward

	err := n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		curForwardID, curForward, err = tx.GetNetworkForward(ctx, n.ID(), memberSpecific, listenAddress)

		return err
	})
	if err != nil {
		return err
	}

	_, err = n.forwardValidate(net.ParseIP(curForward.ListenAddress), req)
	if err != nil {
		return err
	}

	curForwardEtagHash, err := util.EtagHash(curForward.Etag())
	if err != nil {
		return err
	}

	newForward := api.NetworkForward{
		ListenAddress: curForward.ListenAddress,
		Description:   req.Description,
		Config:        req.Config,
		Ports:         req.Ports,
	}

	newForwardEtagHash, err := util.EtagHash(newForward.Etag())
	if err != nil {
		return err
	}

	if curForwardEtagHash == newForwardEtagHash {
		return nil // Nothing has changed.
	}

	revert := revert.New()
	defer revert.Fail()

	err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateNetworkForward(ctx, n.ID(), curForwardID, newForward.Writable())
	})
	if err != nil {
		return err
	}

	revert.Add(func() {
		_ = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateNetworkForward(ctx, n.ID(), curForwardID, curForward.Writable())
		})
		_ = n.forwardSetupFirewall()
		_ = n.forwardBGPSetupPrefixes()
	})

	err = n.forwardSetupFirewall()
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// ForwardDelete deletes a network forward.
func (n *bridge) ForwardDelete(listenAddress string, clientType request.ClientType) error {
	memberSpecific := true // bridge supports per-member forwards.
	var forwardID int64
	var forward *api.NetworkForward

	err := n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		forwardID, forward, err = tx.GetNetworkForward(ctx, n.ID(), memberSpecific, listenAddress)

		return err
	})
	if err != nil {
		return err
	}

	revert := revert.New()
	defer revert.Fail()

	err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.DeleteNetworkForward(ctx, n.ID(), forwardID)
	})
	if err != nil {
		return err
	}

	revert.Add(func() {
		newForward := api.NetworkForwardsPost{
			NetworkForwardPut: forward.Writable(),
			ListenAddress:     forward.ListenAddress,
		}

		_ = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			_, _ = tx.CreateNetworkForward(ctx, n.ID(), memberSpecific, &newForward)

			return nil
		})

		_ = n.forwardSetupFirewall()
		_ = n.forwardBGPSetupPrefixes()
	})

	err = n.forwardSetupFirewall()
	if err != nil {
		return err
	}

	// Refresh exported BGP prefixes on local member.
	err = n.forwardBGPSetupPrefixes()
	if err != nil {
		return fmt.Errorf("Failed applying BGP prefixes for address forwards: %w", err)
	}

	revert.Success()
	return nil
}

// forwardSetupFirewall applies all network address forwards defined for this network and this member.
func (n *bridge) forwardSetupFirewall() error {
	memberSpecific := true // Get all forwards for this cluster member.

	var forwards map[int64]*api.NetworkForward

	err := n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		forwards, err = tx.GetNetworkForwards(ctx, n.ID(), memberSpecific)

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed loading network forwards: %w", err)
	}

	var fwForwards []firewallDrivers.AddressForward
	ipVersions := make(map[uint]struct{})

	for _, forward := range forwards {
		// Convert listen address to subnet so we can check its valid and can be used.
		listenAddressNet, err := ParseIPToNet(forward.ListenAddress)
		if err != nil {
			return fmt.Errorf("Failed parsing address forward listen address %q: %w", forward.ListenAddress, err)
		}

		// Track which IP versions we are using.
		if listenAddressNet.IP.To4() == nil {
			ipVersions[6] = struct{}{}
		} else {
			ipVersions[4] = struct{}{}
		}

		portMaps, err := n.forwardValidate(listenAddressNet.IP, forward.Writable())
		if err != nil {
			return fmt.Errorf("Failed validating firewall address forward for listen address %q: %w", forward.ListenAddress, err)
		}

		fwForwards = append(fwForwards, n.forwardConvertToFirewallForwards(listenAddressNet.IP, net.ParseIP(forward.Config["target_address"]), portMaps)...)
	}

	if len(forwards) > 0 {
		// Check if br_netfilter is enabled to, and warn if not.
		brNetfilterWarning := false
		for ipVersion := range ipVersions {
			err = BridgeNetfilterEnabled(ipVersion)
			if err != nil {
				brNetfilterWarning = true
				msg := fmt.Sprintf("IPv%d bridge netfilter not enabled. Instances using the bridge will not be able to connect to the forward listen IPs", ipVersion)
				n.logger.Warn(msg, logger.Ctx{"err": err})
				err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.UpsertWarningLocalNode(ctx, n.project, entity.TypeNetwork, int(n.id), warningtype.ProxyBridgeNetfilterNotEnabled, fmt.Sprintf("%s: %v", msg, err))
				})
				if err != nil {
					n.logger.Warn("Failed to create warning", logger.Ctx{"err": err})
				}
			}
		}

		if !brNetfilterWarning {
			err = warnings.ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(n.state.DB.Cluster, n.project, warningtype.ProxyBridgeNetfilterNotEnabled, entity.TypeNetwork, int(n.id))
			if err != nil {
				n.logger.Warn("Failed to resolve warning", logger.Ctx{"err": err})
			}
		}
	}

	err = n.state.Firewall.NetworkApplyForwards(n.name, fwForwards)
	if err != nil {
		return fmt.Errorf("Failed applying firewall address forwards: %w", err)
	}

	return nil
}

// Leases returns a list of leases for the bridged network. It will reach out to other cluster members as needed.
// The projectName passed here refers to the initial project from the API request which may differ from the network's project.
// If projectName is empty, get leases from all projects.
func (n *bridge) Leases(projectName string, clientType request.ClientType) ([]api.NetworkLease, error) {
	var err error
	var projectMacs []string
	instanceProjects := make(map[string]string)
	leases := []api.NetworkLease{}

	// Get all static leases.
	if clientType == request.ClientTypeNormal {
		// If requested project matches network's project then include gateway and downstream uplink IPs.
		if projectName == n.project || projectName == "" {
			// Add our own gateway IPs.
			for _, addr := range []string{n.config["ipv4.address"], n.config["ipv6.address"]} {
				ip, _, _ := net.ParseCIDR(addr)
				if ip != nil {
					leases = append(leases, api.NetworkLease{
						Hostname: n.Name() + ".gw",
						Address:  ip.String(),
						Type:     "gateway",
					})
				}
			}

			// Include downstream OVN routers using the network as an uplink.
			var projectNetworks map[string]map[int64]api.Network
			err = n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				projectNetworks, err = tx.GetCreatedNetworks(ctx)
				return err
			})
			if err != nil {
				return nil, err
			}

			// Look for networks using the current network as an uplink.
			for projectName, networks := range projectNetworks {
				for _, network := range networks {
					if network.Config["network"] != n.name {
						continue
					}

					// Found a network, add leases.
					for _, k := range []string{"volatile.network.ipv4.address", "volatile.network.ipv6.address"} {
						v := network.Config[k]
						if v != "" {
							leases = append(leases, api.NetworkLease{
								Hostname: projectName + "-" + network.Name + ".uplink",
								Address:  v,
								Type:     "uplink",
								Project:  projectName,
							})
						}
					}
				}
			}
		}

		// Get all the instances in the requested project that are connected to this network.
		var filter dbCluster.InstanceFilter
		if projectName != "" {
			filter = dbCluster.InstanceFilter{Project: &projectName}
		}

		err = UsedByInstanceDevices(n.state, n.Project(), n.Name(), n.Type(), func(inst db.InstanceArgs, nicName string, nicConfig map[string]string) error {
			// Fill in the hwaddr from volatile.
			if nicConfig["hwaddr"] == "" {
				nicConfig["hwaddr"] = inst.Config[fmt.Sprintf("volatile.%s.hwaddr", nicName)]
			}

			// Keep instance project to use on dynamic leases.
			instanceProjects[inst.Name] = inst.Project

			// Record the MAC.
			hwAddr, _ := net.ParseMAC(nicConfig["hwaddr"])
			if hwAddr != nil {
				projectMacs = append(projectMacs, hwAddr.String())
			}

			// Add the lease.
			nicIP4 := net.ParseIP(nicConfig["ipv4.address"])
			if nicIP4 != nil {
				leases = append(leases, api.NetworkLease{
					Hostname: inst.Name,
					Address:  nicIP4.String(),
					Hwaddr:   hwAddr.String(),
					Type:     "static",
					Location: inst.Node,
					Project:  inst.Project,
				})
			}

			nicIP6 := net.ParseIP(nicConfig["ipv6.address"])
			if nicIP6 != nil {
				leases = append(leases, api.NetworkLease{
					Hostname: inst.Name,
					Address:  nicIP6.String(),
					Hwaddr:   hwAddr.String(),
					Type:     "static",
					Location: inst.Node,
					Project:  inst.Project,
				})
			}

			// Add EUI64 records.
			_, netIP6, _ := net.ParseCIDR(n.config["ipv6.address"])
			if netIP6 != nil && hwAddr != nil && shared.IsFalseOrEmpty(n.config["ipv6.dhcp.stateful"]) {
				eui64IP6, err := eui64.ParseMAC(netIP6.IP, hwAddr)
				if err == nil {
					leases = append(leases, api.NetworkLease{
						Hostname: inst.Name,
						Address:  eui64IP6.String(),
						Hwaddr:   hwAddr.String(),
						Type:     "dynamic",
						Location: inst.Node,
						Project:  inst.Project,
					})
				}
			}

			return nil
		}, filter)
		if err != nil {
			return nil, err
		}
	}

	// Get dynamic leases.
	leaseFile := shared.VarPath("networks", n.name, "dnsmasq.leases")
	if !shared.PathExists(leaseFile) {
		return leases, nil
	}

	content, err := os.ReadFile(leaseFile)
	if err != nil {
		return nil, err
	}

	for lease := range strings.SplitSeq(string(content), "\n") {
		fields := strings.Fields(lease)
		if len(fields) >= 5 {
			// Parse the MAC.
			mac := GetMACSlice(fields[1])
			macStr := strings.Join(mac, ":")

			if len(macStr) < 17 && fields[4] != "" {
				macStr = fields[4][len(fields[4])-17:]
			}

			// Look for an existing static entry.
			found := false
			for _, entry := range leases {
				if entry.Hwaddr == macStr && entry.Address == fields[2] {
					found = true
					break
				}
			}

			if found {
				continue
			}

			// DHCPv6 leases can't be tracked down to a MAC so clear the field.
			// This means that instance project filtering will not work on IPv6 leases.
			if strings.Contains(fields[2], ":") {
				macStr = ""
			}

			// Skip leases that don't match any of the instance MACs from the project (only when we
			// have populated the projectMacs list in ClientTypeNormal mode). Otherwise get all local
			// leases and they will be filtered on the server handling the end user request.
			if clientType == request.ClientTypeNormal && macStr != "" && !slices.Contains(projectMacs, macStr) {
				continue
			}

			// Add the lease to the list.
			leases = append(leases, api.NetworkLease{
				Hostname: fields[3],
				Address:  fields[2],
				Hwaddr:   macStr,
				Type:     "dynamic",
				Location: n.state.ServerName,
				Project:  instanceProjects[fields[3]],
			})
		}
	}

	// Collect leases from other servers.
	if clientType == request.ClientTypeNormal {
		notifier, err := cluster.NewNotifier(n.state, n.state.Endpoints.NetworkCert(), n.state.ServerCert(), cluster.NotifyAll)
		if err != nil {
			return nil, err
		}

		leasesCh := make(chan api.NetworkLease)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			for lease := range leasesCh {
				leases = append(leases, lease)
			}

			wg.Done()
		}()

		err = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
			memberLeases, err := client.GetNetworkLeases(n.name)
			if err != nil {
				return err
			}

			// Add local leases from other members, filtering them for MACs that belong to the project.
			for _, lease := range memberLeases {
				if lease.Hwaddr != "" && slices.Contains(projectMacs, lease.Hwaddr) {
					leasesCh <- lease
				}
			}

			return nil
		})

		// Finish up and wait for go routine.
		close(leasesCh)
		wg.Wait()

		if err != nil {
			return nil, err
		}
	}

	return leases, nil
}

// UsesDNSMasq indicates if network's config indicates if it needs to use dnsmasq.
func (n *bridge) UsesDNSMasq() bool {
	return n.config["bridge.mode"] == "fan" || !slices.Contains([]string{"", "none"}, n.config["ipv4.address"]) || !slices.Contains([]string{"", "none"}, n.config["ipv6.address"])
}

// checkAddressNotInOVNRange checks that a given IP address does not overlap
// with OVN ranges set on this network bridge.
// Returns an error if the check could not be performed or the IP address
// overlaps with OVN ranges.
func (n *bridge) checkAddressNotInOVNRange(addr net.IP) error {
	if addr == nil {
		return errors.New("Invalid listen address")
	}

	addrIsIP4 := addr.To4() != nil

	ovnRangesKey := "ipv4.ovn.ranges"
	if !addrIsIP4 {
		ovnRangesKey = "ipv6.ovn.ranges"
	}

	if n.config[ovnRangesKey] != "" {
		ovnRanges, err := shared.ParseIPRanges(n.config[ovnRangesKey])
		if err != nil {
			return fmt.Errorf("Failed parsing %q: %w", ovnRangesKey, err)
		}

		for _, ovnRange := range ovnRanges {
			if ovnRange.ContainsIP(addr) {
				return fmt.Errorf("Listen address %q overlaps with %q (%q)", addr, ovnRangesKey, ovnRange)
			}
		}
	}

	return nil
}
