(network-sriov)=
# SR-IOV network

The sriov network type allows one to specify presets to use when connecting instances to a parent interface
using sriov NICs. This allows the instance NIC itself to simply specify the `network` it is connecting to without
knowing any of the underlying configuration details.

Network configuration properties:

Key                             | Type      | Condition             | Default                   | Description
:--                             | :--       | :--                   | :--                       | :--
maas.subnet.ipv4                | string    | ipv4 address          | -                         | MAAS IPv4 subnet to register instances in (when using `network` property on nic)
maas.subnet.ipv6                | string    | ipv6 address          | -                         | MAAS IPv6 subnet to register instances in (when using `network` property on nic)
mtu                             | integer   | -                     | -                         | The MTU of the new interface
parent                          | string    | -                     | -                         | Parent interface to create sriov NICs on
vlan                            | integer   | -                     | -                         | The VLAN ID to attach to
