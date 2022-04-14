---
discourse: 7322
---

(network-bridge)=
# Bridge network

As one of the possible network configuration types under LXD, LXD supports creating and managing network bridges.
<!-- Include start bridge intro -->
A network bridge creates a virtual L2 ethernet switch that instance NICs can connect to, making it possible for them to communicate with each other and the host.
LXD bridges can leverage underlying native Linux bridges and Open vSwitch.
<!-- Include end bridge intro -->

The `bridge` network type allows to create an L2 bridge that connects the instances that use it together into a single network L2 segment.
Bridges created by LXD are managed, which means that in addition to creating the bridge interface itself, LXD also sets up a local `dnsmasq` process to provide DHCP, IPv6 route announcements and DNS services to the network.
By default, it also performs NAT for the bridge.

See {ref}`network-bridge-firewalld` for instructions on how to configure firewalld to work with LXD bridge networks.

## IPv6 prefix size

If you're using IPv6 for your bridge network, you should use a prefix size of 64.

Larger subnets (i.e., using a prefix smaller than 64) should work properly too, but they aren't typically that useful for {abbr}`SLAAC (Stateless Address Auto-configuration)`.

Smaller subnets are in theory possible (when using stateful DHCPv6 for IPv6 allocation), but they aren't properly supported by `dnsmasq` and might cause problems.
If you must create a smaller subnet, use static allocation or another standalone router advertisement daemon.

(network-bridge-options)=
## Configuration options

The following configuration key namespaces are currently supported for the `bridge` network type:

 - `bgp` (BGP peer configuration)
 - `bridge` (L2 interface configuration)
 - `dns` (DNS server and resolution configuration)
 - `fan` (configuration specific to the Ubuntu FAN overlay)
 - `ipv4` (L3 IPv4 configuration)
 - `ipv6` (L3 IPv6 configuration)
 - `maas` (MAAS network identification)
 - `security` (network ACL configuration)
 - `raw` (raw configuration file content)
 - `tunnel` (cross-host tunneling configuration)
 - `user` (free-form key/value for user metadata)

```{note}
{{note_ip_addresses_CIDR}}
```

The following configuration options are available for the `bridge` network type:

Key                                  | Type      | Condition             | Default                   | Description
:--                                  | :--       | :--                   | :--                       | :--
bgp.peers.NAME.address               | string    | bgp server            | -                         | Peer address (IPv4 or IPv6)
bgp.peers.NAME.asn                   | integer   | bgp server            | -                         | Peer AS number
bgp.peers.NAME.password              | string    | bgp server            | - (no password)           | Peer session password (optional)
bgp.ipv4.nexthop                     | string    | bgp server            | local address             | Override the next-hop for advertised prefixes
bgp.ipv6.nexthop                     | string    | bgp server            | local address             | Override the next-hop for advertised prefixes
bridge.driver                        | string    | -                     | native                    | Bridge driver: `native` or `openvswitch`
bridge.external\_interfaces          | string    | -                     | -                         | Comma-separated list of unconfigured network interfaces to include in the bridge
bridge.hwaddr                        | string    | -                     | -                         | MAC address for the bridge
bridge.mode                          | string    | -                     | standard                  | Bridge operation mode: `standard` or `fan`
bridge.mtu                           | integer   | -                     | 1500                      | Bridge MTU (default varies if tunnel or fan setup)
dns.domain                           | string    | -                     | lxd                       | Domain to advertise to DHCP clients and use for DNS resolution
dns.mode                             | string    | -                     | managed                   | DNS registration mode: `none` for no DNS record, `managed` for LXD-generated static records or `dynamic` for client-generated records
dns.search                           | string    | -                     | -                         | Full comma-separated domain search list, defaulting to `dns.domain` value
dns.zone.forward                     | string    | -                     | managed                   | DNS zone name for forward DNS records
dns.zone.reverse.ipv4                | string    | -                     | managed                   | DNS zone name for IPv4 reverse DNS records
dns.zone.reverse.ipv6                | string    | -                     | managed                   | DNS zone name for IPv6 reverse DNS records
fan.overlay\_subnet                  | string    | fan mode              | 240.0.0.0/8               | Subnet to use as the overlay for the FAN (CIDR)
fan.type                             | string    | fan mode              | vxlan                     | Tunneling type for the FAN: `vxlan` or `ipip`
fan.underlay\_subnet                 | string    | fan mode              | auto (on create only)     | Subnet to use as the underlay for the FAN (use `auto` to use default gateway subnet) (CIDR)
ipv4.address                         | string    | standard mode         | auto (on create only)     | IPv4 address for the bridge (use `none` to turn off IPv4 or `auto` to generate a new random unused subnet) (CIDR)
ipv4.dhcp                            | boolean   | ipv4 address          | true                      | Whether to allocate addresses using DHCP
ipv4.dhcp.expiry                     | string    | ipv4 dhcp             | 1h                        | When to expire DHCP leases
ipv4.dhcp.gateway                    | string    | ipv4 dhcp             | ipv4.address              | Address of the gateway for the subnet
ipv4.dhcp.ranges                     | string    | ipv4 dhcp             | all addresses             | Comma-separated list of IP ranges to use for DHCP (FIRST-LAST format)
ipv4.firewall                        | boolean   | ipv4 address          | true                      | Whether to generate filtering firewall rules for this network
ipv4.nat                             | boolean   | ipv4 address          | false                     | Whether to NAT (if unset when creating the network, set to `true` for regular bridges when `ipv4.address` is generated and always for fan bridges)
ipv4.nat.address                     | string    | ipv4 address          | -                         | The source address used for outbound traffic from the bridge
ipv4.nat.order                       | string    | ipv4 address          | before                    | Whether to add the required NAT rules before or after any pre-existing rules
ipv4.ovn.ranges                      | string    | -                     | -                         | Comma-separated list of IPv4 ranges to use for child OVN network routers (FIRST-LAST format)
ipv4.routes                          | string    | ipv4 address          | -                         | Comma-separated list of additional IPv4 CIDR subnets to route to the bridge
ipv4.routing                         | boolean   | ipv4 address          | true                      | Whether to route traffic in and out of the bridge
ipv6.address                         | string    | standard mode         | auto (on create only)     | IPv6 address for the bridge (use `none` to turn off IPv6 or `auto` to generate a new random unused subnet) (CIDR)
ipv6.dhcp                            | boolean   | ipv6 address          | true                      | Whether to provide additional network configuration over DHCP
ipv6.dhcp.expiry                     | string    | ipv6 dhcp             | 1h                        | When to expire DHCP leases
ipv6.dhcp.ranges                     | string    | ipv6 stateful dhcp    | all addresses             | Comma-separated list of IPv6 ranges to use for DHCP (FIRST-LAST format)
ipv6.dhcp.stateful                   | boolean   | ipv6 dhcp             | false                     | Whether to allocate addresses using DHCP
ipv6.firewall                        | boolean   | ipv6 address          | true                      | Whether to generate filtering firewall rules for this network
ipv6.nat                             | boolean   | ipv6 address          | false                     | Whether to NAT (if unset when creating the network, set to `true` when `ipv6.address` is generated)
ipv6.nat.address                     | string    | ipv6 address          | -                         | The source address used for outbound traffic from the bridge
ipv6.nat.order                       | string    | ipv6 address          | before                    | Whether to add the required NAT rules before or after any pre-existing rules
ipv6.ovn.ranges                      | string    | -                     | -                         | Comma-separated list of IPv6 ranges to use for child OVN network routers (FIRST-LAST format)
ipv6.routes                          | string    | ipv6 address          | -                         | Comma-separated list of additional IPv6 CIDR subnets to route to the bridge
ipv6.routing                         | boolean   | ipv6 address          | true                      | Whether to route traffic in and out of the bridge
maas.subnet.ipv4                     | string    | ipv4 address          | -                         | MAAS IPv4 subnet to register instances in (when using `network` property on NIC)
maas.subnet.ipv6                     | string    | ipv6 address          | -                         | MAAS IPv6 subnet to register instances in (when using `network` property on NIC)
raw.dnsmasq                          | string    | -                     | -                         | Additional `dnsmasq` configuration to append to the configuration file
security.acls                        | string    | -                     | -                         | Comma-separated list of Network ACLs to apply to NICs connected to this network (see {ref}`network-acls-bridge-limitations`)
security.acls.default.egress.action  | string    | security.acls         | reject                    | Action to use for egress traffic that doesn't match any ACL rule
security.acls.default.egress.logged  | boolean   | security.acls         | false                     | Whether to log egress traffic that doesn't match any ACL rule
security.acls.default.ingress.action | string    | security.acls         | reject                    | Action to use for ingress traffic that doesn't match any ACL rule
security.acls.default.ingress.logged | boolean   | security.acls         | false                     | Whether to log ingress traffic that doesn't match any ACL rule
tunnel.NAME.group                    | string    | vxlan                 | 239.0.0.1                 | Multicast address for vxlan (used if local and remote aren't set)
tunnel.NAME.id                       | integer   | vxlan                 | 0                         | Specific tunnel ID to use for the vxlan tunnel
tunnel.NAME.interface                | string    | vxlan                 | -                         | Specific host interface to use for the tunnel
tunnel.NAME.local                    | string    | gre or vxlan          | -                         | Local address for the tunnel (not necessary for multicast vxlan)
tunnel.NAME.port                     | integer   | vxlan                 | 0                         | Specific port to use for the vxlan tunnel
tunnel.NAME.protocol                 | string    | standard mode         | -                         | Tunneling protocol: `vxlan` or `gre`
tunnel.NAME.remote                   | string    | gre or vxlan          | -                         | Remote address for the tunnel (not necessary for multicast vxlan)
tunnel.NAME.ttl                      | integer   | vxlan                 | 1                         | Specific TTL to use for multicast routing topologies
user.*                               | string    | -                     | -                         | User-provided free-form key/value pairs

(network-bridge-features)=
## Supported features

The following features are supported for the `bridge` network type:

- {ref}`network-acls`
- {ref}`network-forwards`
- {ref}`network-zones`
- {ref}`network-bgp`
- {ref}`network-bridge-resolved`


```{toctree}
:maxdepth: 1
:hidden:

Integrate with systemd-resolved </howto/network_bridge_resolved>
Configure Firewalld </howto/network_bridge_firewalld>
```
