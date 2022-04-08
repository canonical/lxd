(network-macvlan)=
# Macvlan network

<!-- Include start macvlan intro -->
Macvlan is a virtual {abbr}`LAN (Local Area Network)` that you can use if you want to assign several IP addresses to the same network interface, basically splitting up the network interface into several sub-interfaces with their own IP addresses.
You can then assign IP addresses based on the randomly generated MAC addresses.
<!-- Include end macvlan intro -->

The macvlan network type allows one to specify presets to use when connecting instances to a parent interface
using macvlan NICs. This allows the instance NIC itself to simply specify the `network` it is connecting to without
knowing any of the underlying configuration details.

(network-macvlan-options)=
## Configuration options

Key                             | Type      | Condition             | Default                   | Description
:--                             | :--       | :--                   | :--                       | :--
gvrp                            | boolean   | -                     | false                     | Register VLAN using GARP VLAN Registration Protocol
mtu                             | integer   | -                     | -                         | The MTU of the new interface
parent                          | string    | -                     | -                         | Parent interface to create macvlan NICs on
vlan                            | integer   | -                     | -                         | The VLAN ID to attach to
maas.subnet.ipv4                | string    | ipv4 address          | -                         | MAAS IPv4 subnet to register instances in (when using `network` property on nic)
maas.subnet.ipv6                | string    | ipv6 address          | -                         | MAAS IPv6 subnet to register instances in (when using `network` property on nic)
