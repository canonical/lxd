package device

import (
	"fmt"
	"strings"

	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/network/acl"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/validate"
)

// nicValidationRules returns config validation rules for nic devices.
func nicValidationRules(requiredFields []string, optionalFields []string, instConf instance.ConfigReader) map[string]func(value string) error {
	// Define a set of default validators for each field name.
	defaultValidators := map[string]func(value string) error{
		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=acceleration)
		// Possible values are `none`, `sriov`, or `vdpa`.
		// See {ref}`devices-nic-hw-acceleration` for more information.
		// ---
		//  type: string
		//  defaultdesc: `none`
		//  managed: no
		//  shortdesc: Enable hardware offloading
		"acceleration": validate.Optional(validate.IsOneOf("none", "sriov", "vdpa")),
		// lxdmeta:generate(entities=device-nic-{bridged+macvlan+sriov+physical+ovn}; group=device-conf; key=name)
		//
		// ---
		//  type: string
		//  defaultdesc: kernel assigned
		//  managed: no
		//  shortdesc: Name of the interface inside the instance

		// lxdmeta:generate(entities=device-nic-{ipvlan+p2p+routed}; group=device-conf; key=name)
		//
		// ---
		//  type: string
		//  defaultdesc: kernel assigned
		//  shortdesc: Name of the interface inside the instance
		"name": validate.Optional(validate.IsInterfaceName, func(_ string) error { return nicCheckNamesUnique(instConf) }),
		// lxdmeta:generate(entities=device-nic-{bridged+macvlan+sriov+physical}; group=device-conf; key=parent)
		//
		// ---
		//  type: string
		//  managed: yes
		//  required: if specifying the `nictype` directly
		//  shortdesc: Name of the host device

		// lxdmeta:generate(entities=device-nic-ipvlan; group=device-conf; key=parent)
		//
		// ---
		//  type: string
		//  required: yes
		//  shortdesc: Name of the host device

		// lxdmeta:generate(entities=device-nic-routed; group=device-conf; key=parent)
		//
		// ---
		//  type: string
		//  shortdesc: Name of the host device to join the instance to
		"parent": validate.IsAny,
		// lxdmeta:generate(entities=device-nic-{bridged+macvlan+sriov+physical}; group=device-conf; key=network)
		// You can specify this option instead of specifying the `nictype` directly.
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: Managed network to link the device to

		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=network)
		//
		// ---
		//  type: string
		//  managed: yes
		//  required: yes
		//  shortdesc: Managed network to link the device to
		"network": validate.IsAny,
		// lxdmeta:generate(entities=device-nic-{bridged+macvlan}; group=device-conf; key=mtu)
		//
		// ---
		//  type: integer
		//  defaultdesc: parent MTU
		//  managed: yes
		//  shortdesc: MTU of the new interface

		// lxdmeta:generate(entities=device-nic-sriov; group=device-conf; key=mtu)
		//
		// ---
		//  type: integer
		//  defaultdesc: kernel assigned
		//  managed: yes
		//  shortdesc: MTU of the new interface

		// lxdmeta:generate(entities=device-nic-physical; group=device-conf; key=mtu)
		//
		// ---
		//  type: integer
		//  defaultdesc: parent MTU
		//  managed: no
		//  shortdesc: MTU of the new interface

		// lxdmeta:generate(entities=device-nic-{ipvlan+routed}; group=device-conf; key=mtu)
		//
		// ---
		//  type: integer
		//  defaultdesc: parent MTU
		//  shortdesc: The MTU of the new interface

		// lxdmeta:generate(entities=device-nic-p2p; group=device-conf; key=mtu)
		//
		// ---
		//  type: integer
		//  defaultdesc: kernel assigned
		//  shortdesc: MTU of the new interface
		"mtu": validate.Optional(validate.IsNetworkMTU),
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=vlan)
		// Set this option to `none` to remove the port from the default VLAN.
		// ---
		//  type: integer
		//  managed: no
		//  shortdesc: VLAN ID to use for non-tagged traffic

		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=vlan.tagged)
		// Specify the VLAN IDs or ranges as a comma-delimited list.
		// ---
		//  type: integer
		//  managed: no
		//  shortdesc: VLAN IDs or VLAN ranges to join for tagged traffic

		// lxdmeta:generate(entities=device-nic-{macvlan+sriov+physical}; group=device-conf; key=vlan)
		//
		// ---
		//  type: integer
		//  managed: no
		//  shortdesc: VLAN ID to attach to

		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=vlan)
		// See also {config:option}`device-nic-ovn-device-conf:nested`.
		// ---
		//  type: integer
		//  managed: no
		//  shortdesc: VLAN ID to use when nesting

		// lxdmeta:generate(entities=device-nic-{ipvlan+routed}; group=device-conf; key=vlan)
		//
		// ---
		//  type: integer
		//  shortdesc: VLAN ID to attach to
		"vlan": validate.IsNetworkVLAN,
		// lxdmeta:generate(entities=device-nic-{macvlan+physical}; group=device-conf; key=gvrp)
		// This option specifies whether to register the VLAN using the GARP VLAN Registration Protocol.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  managed: no
		//  shortdesc: Whether to use GARP VLAN Registration Protocol

		// lxdmeta:generate(entities=device-nic-{ipvlan+routed}; group=device-conf; key=gvrp)
		// This option specifies whether to register the VLAN using the GARP VLAN Registration Protocol.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  shortdesc: Whether to use GARP VLAN Registration Protocol
		"gvrp": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=device-nic-{bridged+macvlan+sriov+physical+ovn}; group=device-conf; key=hwaddr)
		//
		// ---
		//  type: string
		//  defaultdesc: randomly assigned
		//  managed: no
		//  shortdesc: MAC address of the new interface

		// lxdmeta:generate(entities=device-nic-{ipvlan+p2p+routed}; group=device-conf; key=hwaddr)
		//
		// ---
		//  type: string
		//  defaultdesc: randomly assigned
		//  shortdesc: MAC address of the new interface
		"hwaddr": validate.IsNetworkMAC,
		// lxdmeta:generate(entities=device-nic-{bridged+ovn}; group=device-conf; key=host_name)
		//
		// ---
		//  type: string
		//  defaultdesc: randomly assigned
		//  managed: no
		//  shortdesc: Name of the interface inside the host

		// lxdmeta:generate(entities=device-nic-{p2p+routed}; group=device-conf; key=host_name)
		//
		// ---
		//  type: string
		//  defaultdesc: randomly assigned
		//  shortdesc: Name of the interface inside the host
		"host_name": validate.IsAny,
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=limits.ingress)
		// Specify the limit in bit/s. Various suffixes are supported (see {ref}`instances-limit-units`).
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: I/O limit for incoming traffic

		// lxdmeta:generate(entities=device-nic-{p2p+routed}; group=device-conf; key=limits.ingress)
		// Specify the limit in bit/s. Various suffixes are supported (see {ref}`instances-limit-units`).
		// ---
		//  type: string
		//  shortdesc: I/O limit for incoming traffic
		"limits.ingress": validate.IsAny,
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=limits.egress)
		// Specify the limit in bit/s. Various suffixes are supported (see {ref}`instances-limit-units`).
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: I/O limit for outgoing traffic

		// lxdmeta:generate(entities=device-nic-{p2p+routed}; group=device-conf; key=limits.egress)
		// Specify the limit in bit/s. Various suffixes are supported (see {ref}`instances-limit-units`).
		// ---
		//  type: string
		//  shortdesc: I/O limit for outgoing traffic
		"limits.egress": validate.IsAny,
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=limits.max)
		// This option is the same as setting both {config:option}`device-nic-bridged-device-conf:limits.ingress` and {config:option}`device-nic-bridged-device-conf:limits.egress`.
		//
		// Specify the limit in bit/s. Various suffixes are supported (see {ref}`instances-limit-units`).
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: I/O limit for both incoming and outgoing traffic

		// lxdmeta:generate(entities=device-nic-{p2p+routed}; group=device-conf; key=limits.max)
		// This option is the same as setting both {config:option}`device-nic-bridged-device-conf:limits.ingress` and {config:option}`device-nic-bridged-device-conf:limits.egress`.
		//
		// Specify the limit in bit/s. Various suffixes are supported (see {ref}`instances-limit-units`).
		// ---
		//  type: string
		//  shortdesc: I/O limit for both incoming and outgoing traffic
		"limits.max": validate.IsAny,
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=limits.priority)
		// The `skb->priority` value for outgoing traffic is used by the kernel queuing discipline (qdisc) to prioritize network packets.
		// Specify the value as a 32-bit unsigned integer.
		//
		// The effect of this value depends on the particular qdisc implementation, for example, `SKBPRIO` or `QFQ`.
		// Consult the kernel qdisc documentation before setting this value.
		// ---
		//  type: integer
		//  managed: no
		//  shortdesc: `skb->priority` value for outgoing traffic

		// lxdmeta:generate(entities=device-nic-{p2p+routed}; group=device-conf; key=limits.priority)
		// The `skb->priority` value for outgoing traffic is used by the kernel queuing discipline (qdisc) to prioritize network packets.
		// Specify the value as a 32-bit unsigned integer.
		//
		// The effect of this value depends on the particular qdisc implementation, for example, `SKBPRIO` or `QFQ`.
		// Consult the kernel qdisc documentation before setting this value.
		// ---
		//  type: integer
		//  shortdesc: `skb->priority` value for outgoing traffic
		"limits.priority": validate.Optional(validate.IsUint32),
		// lxdmeta:generate(entities=device-nic-{bridged+sriov}; group=device-conf; key=security.mac_filtering)
		// Set this option to `true` to prevent the instance from spoofing another instance’s MAC address.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  managed: no
		//  shortdesc: Whether to prevent the instance from spoofing a MAC address
		"security.mac_filtering": validate.IsAny,
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=security.ipv4_filtering)
		// Set this option to `true` to prevent the instance from spoofing another instance’s IPv4 address.
		// This option enables {config:option}`device-nic-bridged-device-conf:security.mac_filtering`.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  managed: no
		//  shortdesc: Whether to prevent the instance from spoofing an IPv4 address
		"security.ipv4_filtering": validate.IsAny,
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=security.ipv6_filtering)
		// Set this option to `true` to prevent the instance from spoofing another instance’s IPv6 address.
		// This option enables {config:option}`device-nic-bridged-device-conf:security.mac_filtering`.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  managed: no
		//  shortdesc: Whether to prevent the instance from spoofing an IPv6 address
		"security.ipv6_filtering": validate.IsAny,
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=security.port_isolation)
		// Set this option to `true` to prevent the NIC from communicating with other NICs in the network that have port isolation enabled.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  managed: no
		//  shortdesc: Whether to respect port isolation
		"security.port_isolation": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=device-nic-{bridged+macvlan+sriov}; group=device-conf; key=maas.subnet.ipv4)
		//
		// ---
		//  type: string
		//  managed: yes
		//  shortdesc: MAAS IPv4 subnet to register the instance in

		// lxdmeta:generate(entities=device-nic-physical; group=device-conf; key=maas.subnet.ipv4)
		//
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: MAAS IPv4 subnet to register the instance in
		"maas.subnet.ipv4": validate.IsAny,
		// lxdmeta:generate(entities=device-nic-{bridged+macvlan+sriov}; group=device-conf; key=maas.subnet.ipv6)
		//
		// ---
		//  type: string
		//  managed: yes
		//  shortdesc: MAAS IPv6 subnet to register the instance in

		// lxdmeta:generate(entities=device-nic-physical; group=device-conf; key=maas.subnet.ipv6)
		//
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: MAAS IPv6 subnet to register the instance in
		"maas.subnet.ipv6": validate.IsAny,
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=ipv4.address)
		// Set this option to `none` to restrict all IPv4 traffic when {config:option}`device-nic-bridged-device-conf:security.ipv4_filtering` is set.
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: IPv4 address to assign to the instance through DHCP

		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=ipv4.address)
		//
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: IPv4 address to assign to the instance through DHCP

		// lxdmeta:generate(entities=device-nic-ipvlan; group=device-conf; key=ipv4.address)
		// Specify a comma-delimited list of IPv4 static addresses to add to the instance.
		// In `l2` mode, you can specify them as CIDR values or singular addresses using a subnet of `/24`.
		// ---
		//  type: string
		//  shortdesc: IPv4 static addresses to add to the instance

		// lxdmeta:generate(entities=device-nic-routed; group=device-conf; key=ipv4.address)
		// Specify a comma-delimited list of IPv4 static addresses to add to the instance.
		// ---
		//  type: string
		//  shortdesc: IPv4 static addresses to add to the instance
		"ipv4.address": validate.Optional(validate.IsNetworkAddressV4),
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=ipv6.address)
		// Set this option to `none` to restrict all IPv6 traffic when {config:option}`device-nic-bridged-device-conf:security.ipv6_filtering` is set.
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: IPv6 address to assign to the instance through DHCP

		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=ipv6.address)
		//
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: IPv6 address to assign to the instance through DHCP

		// lxdmeta:generate(entities=device-nic-ipvlan; group=device-conf; key=ipv6.address)
		// Specify a comma-delimited list of IPv6 static addresses to add to the instance.
		// In `l2` mode, you can specify them as CIDR values or singular addresses using a subnet of `/64`.
		// ---
		//  type: string
		//  shortdesc: IPv6 static addresses to add to the instance

		// lxdmeta:generate(entities=device-nic-routed; group=device-conf; key=ipv6.address)
		// Specify a comma-delimited list of IPv6 static addresses to add to the instance.
		// ---
		//  type: string
		//  shortdesc: IPv6 static addresses to add to the instance
		"ipv6.address": validate.Optional(validate.IsNetworkAddressV6),
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=ipv4.routes)
		// Specify a comma-delimited list of IPv4 static routes for this NIC to add on the host.
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: IPv4 static routes for the NIC to add on the host

		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=ipv4.routes)
		// Specify a comma-delimited list of IPv4 static routes to route for this NIC.
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: IPv4 static routes to route for the NIC

		// lxdmeta:generate(entities=device-nic-routed; group=device-conf; key=ipv4.routes)
		// Specify a comma-delimited list of IPv4 static routes for this NIC to add on the host (without L2 ARP/NDP proxy).
		// ---
		//  type: string
		//  shortdesc: IPv4 static routes for the NIC to add on the host

		// lxdmeta:generate(entities=device-nic-p2p; group=device-conf; key=ipv4.routes)
		// Specify a comma-delimited list of IPv4 static routes for this NIC to add on the host.
		// ---
		//  type: string
		//  shortdesc: IPv4 static routes for the NIC to add on the host
		"ipv4.routes": validate.Optional(validate.IsListOf(validate.IsNetworkV4)),
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=ipv6.routes)
		// Specify a comma-delimited list of IPv6 static routes for this NIC to add on the host.
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: IPv6 static routes for the NIC to add on the host

		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=ipv6.routes)
		// Specify a comma-delimited list of IPv6 static routes to route to the NIC.
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: IPv6 static routes to route to the NIC

		// lxdmeta:generate(entities=device-nic-p2p; group=device-conf; key=ipv6.routes)
		// Specify a comma-delimited list of IPv6 static routes for this NIC to add on the host.
		// ---
		//  type: string
		//  shortdesc: IPv6 static routes for the NIC to add on the host

		// lxdmeta:generate(entities=device-nic-routed; group=device-conf; key=ipv6.routes)
		// Specify a comma-delimited list of IPv6 static routes for this NIC to add on the host (without L2 ARP/NDP proxy).
		// ---
		//  type: string
		//  shortdesc: IPv6 static routes for the NIC to add on the host
		"ipv6.routes": validate.Optional(validate.IsListOf(validate.IsNetworkV6)),
		// lxdmeta:generate(entities=device-nic-{bridged+macvlan+sriov+physical+ovn}; group=device-conf; key=boot.priority)
		// A higher value for this option means that the VM boots first.
		// ---
		//  type: integer
		//  managed: no
		//  shortdesc: Boot priority for VMs

		// lxdmeta:generate(entities=device-nic-p2p; group=device-conf; key=boot.priority)
		// A higher value for this option means that the VM boots first.
		// ---
		//  type: integer
		//  shortdesc: Boot priority for VMs
		"boot.priority": validate.Optional(validate.IsUint32),
		// lxdmeta:generate(entities=device-nic-ipvlan; group=device-conf; key=ipv4.gateway)
		// In `l3s` mode, the option specifies whether to add an automatic default IPv4 gateway.
		// Possible values are `auto` and `none`.
		//
		// In `l2` mode, this option specifies the IPv4 address of the gateway.
		// ---
		//  type: string
		//  defaultdesc: `auto` (`l3s`), `-` (`l2`)
		//  shortdesc: IPv4 gateway

		// lxdmeta:generate(entities=device-nic-routed; group=device-conf; key=ipv4.gateway)
		// Possible values are `auto` and `none`.
		// ---
		//  type: string
		//  defaultdesc: `auto`
		//  shortdesc: Whether to add an automatic default IPv4 gateway
		"ipv4.gateway": networkValidGateway,
		// lxdmeta:generate(entities=device-nic-ipvlan; group=device-conf; key=mode)
		// Possible values are `l2` and `l3s`.
		// ---
		//  type: string
		//  defaultdesc: `l3s`
		//  shortdesc: IPVLAN mode

		// lxdmeta:generate(entities=device-nic-ipvlan; group=device-conf; key=ipv6.gateway)
		// In `l3s` mode, the option specifies whether to add an automatic default IPv6 gateway.
		// Possible values are `auto` and `none`.
		//
		// In `l2` mode, this option specifies the IPv6 address of the gateway.
		// ---
		//  type: string
		//  defaultdesc: `auto` (`l3s`), `-` (`l2`)
		//  shortdesc: IPv6 gateway

		// lxdmeta:generate(entities=device-nic-routed; group=device-conf; key=ipv6.gateway)
		// Possible values are `auto` and `none`.
		// ---
		//  type: string
		//  defaultdesc: `auto`
		//  shortdesc: Whether to add an automatic default IPv6 gateway
		"ipv6.gateway": networkValidGateway,
		// lxdmeta:generate(entities=device-nic-routed; group=device-conf; key=ipv4.host_address)
		//
		// ---
		//  type: string
		//  defaultdesc: `169.254.0.1`
		//  shortdesc: IPv4 address to add to the host-side `veth` interface
		"ipv4.host_address": validate.Optional(validate.IsNetworkAddressV4),
		// lxdmeta:generate(entities=device-nic-routed; group=device-conf; key=ipv6.host_address)
		//
		// ---
		//  type: string
		//  defaultdesc: `fe80::1`
		//  shortdesc: IPv6 address to add to the host-side `veth` interface
		"ipv6.host_address": validate.Optional(validate.IsNetworkAddressV6),
		// lxdmeta:generate(entities=device-nic-{ipvlan+routed}; group=device-conf; key=ipv4.host_table)
		// The custom policy routing table is in addition to the main routing table.
		// ---
		//  type: integer
		//  shortdesc: Custom policy routing table ID to add IPv4 static routes to
		"ipv4.host_table": validate.Optional(validate.IsUint32),
		// lxdmeta:generate(entities=device-nic-{ipvlan+routed}; group=device-conf; key=ipv6.host_table)
		// The custom policy routing table is in addition to the main routing table.
		// ---
		//  type: integer
		//  shortdesc: Custom policy routing table ID to add IPv6 static routes to
		"ipv6.host_table": validate.Optional(validate.IsUint32),
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=queue.tx.length)
		//
		// ---
		//  type: integer
		//  managed: no
		//  shortdesc: Transmit queue length for the NIC

		// lxdmeta:generate(entities=device-nic-{p2p+routed}; group=device-conf; key=queue.tx.length)
		//
		// ---
		//  type: integer
		//  shortdesc: Transmit queue length for the NIC
		"queue.tx.length": validate.Optional(validate.IsUint32),
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=ipv4.routes.external)
		// Specify a comma-delimited list of IPv4 static routes to route to the NIC and publish on the uplink network (BGP).
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: IPv4 static routes to route to NIC

		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=ipv4.routes.external)
		// Specify a comma-delimited list of IPv4 static routes to route to the NIC and publish on the uplink network.
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: IPv4 static routes to route to NIC
		"ipv4.routes.external": validate.Optional(validate.IsListOf(validate.IsNetworkV4)),
		// lxdmeta:generate(entities=device-nic-bridged; group=device-conf; key=ipv6.routes.external)
		// Specify a comma-delimited list of IPv6 static routes to route to the NIC and publish on the uplink network (BGP).
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: IPv6 static routes to route to NIC

		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=ipv6.routes.external)
		// Specify a comma-delimited list of IPv6 static routes to route to the NIC and publish on the uplink network.
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: IPv6 static routes to route to NIC
		"ipv6.routes.external": validate.Optional(validate.IsListOf(validate.IsNetworkV6)),
		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=nested)
		// See also {config:option}`device-nic-ovn-device-conf:vlan`.
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: Parent NIC name to nest this NIC under
		"nested": validate.IsAny,
		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=security.acls)
		// Specify a comma-separated list
		// ---
		//  type: string
		//  managed: no
		//  shortdesc: Network ACLs to apply
		"security.acls": validate.IsAny,
		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=security.acls.default.ingress.action)
		// The specified action is used for all ingress traffic that doesn’t match any ACL rule.
		// ---
		//  type: string
		//  defaultdesc: `reject`
		//  managed: no
		//  shortdesc: Default action to use for ingress traffic
		"security.acls.default.ingress.action": validate.Optional(validate.IsOneOf(acl.ValidActions...)),
		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=security.acls.default.egress.action)
		// The specified action is used for all egress traffic that doesn’t match any ACL rule.
		// ---
		//  type: string
		//  defaultdesc: `reject`
		//  managed: no
		//  shortdesc: Default action to use for egress traffic
		"security.acls.default.egress.action": validate.Optional(validate.IsOneOf(acl.ValidActions...)),
		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=security.acls.default.ingress.logged)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  managed: no
		//  shortdesc: Whether to log ingress traffic that doesn’t match any ACL rule
		"security.acls.default.ingress.logged": validate.Optional(validate.IsBool),
		// lxdmeta:generate(entities=device-nic-ovn; group=device-conf; key=security.acls.default.egress.logged)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  managed: no
		//  shortdesc: Whether to log egress traffic that doesn’t match any ACL rule
		"security.acls.default.egress.logged": validate.Optional(validate.IsBool),
	}

	validators := map[string]func(value string) error{}

	for _, k := range optionalFields {
		defaultValidator := defaultValidators[k]

		// If field doesn't have a known validator, it is an unknown field, skip.
		if defaultValidator == nil {
			continue
		}

		// Wrap the default validator in an empty check as field is optional.
		validators[k] = func(value string) error {
			if value == "" {
				return nil
			}

			return defaultValidator(value)
		}
	}

	// Add required fields last, that way if they are specified in both required and optional
	// field sets, the required one will overwrite the optional validators.
	for _, k := range requiredFields {
		defaultValidator := defaultValidators[k]

		// If field doesn't have a known validator, it is an unknown field, skip.
		if defaultValidator == nil {
			continue
		}

		// Wrap the default validator in a not empty check as field is required.
		validators[k] = func(value string) error {
			err := validate.IsNotEmpty(value)
			if err != nil {
				return err
			}

			return defaultValidator(value)
		}
	}

	return validators
}

// nicHasAutoGateway takes the value of the "ipv4.gateway" or "ipv6.gateway" config keys and returns whether they
// specify whether the gateway mode is automatic or not.
func nicHasAutoGateway(value string) bool {
	if value == "" || value == "auto" {
		return true
	}

	return false
}

// nicCheckNamesUnique checks that all the NICs in the instConf's expanded devices have a unique (or unset) name.
func nicCheckNamesUnique(instConf instance.ConfigReader) error {
	seenNICNames := []string{}

	for _, devConfig := range instConf.ExpandedDevices() {
		if devConfig["type"] != "nic" || devConfig["name"] == "" {
			continue
		}

		if shared.ValueInSlice(devConfig["name"], seenNICNames) {
			return fmt.Errorf("Duplicate NIC name detected %q", devConfig["name"])
		}

		seenNICNames = append(seenNICNames, devConfig["name"])
	}

	return nil
}

// nicCheckDNSNameConflict returns if instNameA matches instNameB (case insensitive).
func nicCheckDNSNameConflict(instNameA string, instNameB string) bool {
	return strings.EqualFold(instNameA, instNameB)
}
