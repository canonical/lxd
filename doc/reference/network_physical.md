(network-physical)=
# Physical network

The physical network type allows one to specify presets to use when connecting OVN networks to a parent interface or to allow an instance to use a physical interface as a NIC.
This allows the instance NIC itself to simply specify the `network` it is connecting to without knowing any of the underlying configuration details.

(network-physical-options)=
## Configuration options

Key                             | Type      | Condition             | Default                   | Description
:--                             | :--       | :--                   | :--                       | :--
bgp.peers.NAME.address          | string    | bgp server            | -                         | Peer address (IPv4 or IPv6) for use by `ovn` downstream networks
bgp.peers.NAME.asn              | integer   | bgp server            | -                         | Peer AS number for use by `ovn` downstream networks
bgp.peers.NAME.password         | string    | bgp server            | - (no password)           | Peer session password (optional) for use by `ovn` downstream networks
maas.subnet.ipv4                | string    | ipv4 address          | -                         | MAAS IPv4 subnet to register instances in (when using `network` property on nic)
maas.subnet.ipv6                | string    | ipv6 address          | -                         | MAAS IPv6 subnet to register instances in (when using `network` property on nic)
mtu                             | integer   | -                     | -                         | The MTU of the new interface
parent                          | string    | -                     | -                         | Parent interface to create sriov NICs on
vlan                            | integer   | -                     | -                         | The VLAN ID to attach to
gvrp                            | boolean   | -                     | false                     | Register VLAN using GARP VLAN Registration Protocol
ipv4.gateway                    | string    | standard mode         | -                         | IPv4 address for the gateway and network (CIDR notation)
ipv4.ovn.ranges                 | string    | -                     | -                         | Comma separate list of IPv4 ranges to use for child OVN network routers (FIRST-LAST format)
ipv4.routes                     | string    | ipv4 address          | -                         | Comma separated list of additional IPv4 CIDR subnets that can be used with child OVN networks ipv4.routes.external setting
ipv4.routes.anycast             | boolean   | ipv4 address          | false                     | Allow the overlapping routes to be used on multiple networks/NIC at the same time.
ipv6.gateway                    | string    | standard mode         | -                         | IPv6 address for the gateway and network  (CIDR notation)
ipv6.ovn.ranges                 | string    | -                     | -                         | Comma separate list of IPv6 ranges to use for child OVN network routers (FIRST-LAST format)
ipv6.routes                     | string    | ipv6 address          | -                         | Comma separated list of additional IPv6 CIDR subnets that can be used with child OVN networks ipv6.routes.external setting
ipv6.routes.anycast             | boolean   | ipv6 address          | false                     | Allow the overlapping routes to be used on multiple networks/NIC at the same time.
dns.nameservers                 | string    | standard mode         | -                         | List of DNS server IPs on physical network
ovn.ingress\_mode               | string    | standard mode         | l2proxy                   | Sets the method that OVN NIC external IPs will be advertised on uplink network. Either `l2proxy` (proxy ARP/NDP) or `routed`.
