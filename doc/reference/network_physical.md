(network-physical)=
# Physical network

<!-- Include start physical intro -->
The `physical` network type connects to an existing physical network, which can be a network card or a bridge, and serves as an uplink network for OVN.
<!-- Include end physical intro -->

The physical network type allows one to specify presets to use when connecting OVN networks to a parent interface or to allow an instance to use a physical interface as a NIC.
This allows the instance NIC itself to simply specify the `network` it is connecting to without knowing any of the underlying configuration details.

(network-physical-options)=
## Configuration options

The following configuration options are available for the `physical` network type:

Key                             | Type      | Condition             | Default                   | Description
:--                             | :--       | :--                   | :--                       | :--
gvrp                            | boolean   | -                     | false                     | Register VLAN using GARP VLAN Registration Protocol
mtu                             | integer   | -                     | -                         | The MTU of the new interface
parent                          | string    | -                     | -                         | Existing interface to use for network
vlan                            | integer   | -                     | -                         | The VLAN ID to attach to
bgp.peers.NAME.address          | string    | bgp server            | -                         | Peer address (IPv4 or IPv6) for use by `ovn` downstream networks
bgp.peers.NAME.asn              | integer   | bgp server            | -                         | Peer AS number for use by `ovn` downstream networks
bgp.peers.NAME.password         | string    | bgp server            | - (no password)           | Peer session password (optional) for use by `ovn` downstream networks
dns.nameservers                 | string    | standard mode         | -                         | List of DNS server IPs on `physical` network
ipv4.gateway                    | string    | standard mode         | -                         | IPv4 address for the gateway and network (CIDR)
ipv4.ovn.ranges                 | string    | -                     | -                         | Comma-separated list of IPv4 ranges to use for child OVN network routers (FIRST-LAST format)
ipv4.routes                     | string    | ipv4 address          | -                         | Comma-separated list of additional IPv4 CIDR subnets that can be used with child OVN networks `ipv4.routes.external` setting
ipv4.routes.anycast             | boolean   | ipv4 address          | false                     | Allow the overlapping routes to be used on multiple networks/NIC at the same time
ipv6.gateway                    | string    | standard mode         | -                         | IPv6 address for the gateway and network (CIDR)
ipv6.ovn.ranges                 | string    | -                     | -                         | Comma-separated list of IPv6 ranges to use for child OVN network routers (FIRST-LAST format)
ipv6.routes                     | string    | ipv6 address          | -                         | Comma-separated list of additional IPv6 CIDR subnets that can be used with child OVN networks `ipv6.routes.external` setting
ipv6.routes.anycast             | boolean   | ipv6 address          | false                     | Allow the overlapping routes to be used on multiple networks/NIC at the same time
maas.subnet.ipv4                | string    | ipv4 address          | -                         | MAAS IPv4 subnet to register instances in (when using `network` property on NIC)
maas.subnet.ipv6                | string    | ipv6 address          | -                         | MAAS IPv6 subnet to register instances in (when using `network` property on NIC)
ovn.ingress\_mode               | string    | standard mode         | l2proxy                   | Sets the method how OVN NIC external IPs will be advertised on uplink network: `l2proxy` (proxy ARP/NDP) or `routed`
user.*                          | string    | -                     | -                         | User-provided free-form key/value pairs
