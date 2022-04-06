---
discourse: 11033
---

(network-ovn)=
# OVN network

The ovn network type allows the creation of logical networks using the OVN SDN. This can be useful for labs and
multi-tenant environments where the same logical subnets are used in multiple discrete networks.

A LXD OVN network can be connected to an existing managed LXD bridge network in order for it to gain outbound
access to the wider network. All connections from the OVN logical networks are NATed to a dynamic IP allocated by
the parent network.

```{toctree}
:maxdepth: 1

Set up OVN </howto/network_ovn_setup>
Create routing relationships </howto/network_ovn_peers>
```

(network-ovn-options)=
## Configuration options

Key                                  | Type      | Condition             | Default                   | Description
:--                                  | :--       | :--                   | :--                       | :--
network                              | string    | -                     | -                         | Uplink network to use for external network access
bridge.hwaddr                        | string    | -                     | -                         | MAC address for the bridge
bridge.mtu                           | integer   | -                     | 1442                      | Bridge MTU (default allows host to host geneve tunnels)
dns.domain                           | string    | -                     | lxd                       | Domain to advertise to DHCP clients and use for DNS resolution
dns.search                           | string    | -                     | -                         | Full comma separated domain search list, defaulting to `dns.domain` value
dns.zone.forward                     | string    | -                     | -                         | DNS zone name for forward DNS records
dns.zone.reverse.ipv4                | string    | -                     | -                         | DNS zone name for IPv4 reverse DNS records
dns.zone.reverse.ipv6                | string    | -                     | -                         | DNS zone name for IPv6 reverse DNS records
ipv4.address                         | string    | standard mode         | auto (on create only)     | IPv4 address for the bridge (CIDR notation). Use "none" to turn off IPv4 or "auto" to generate a new random unused subnet
ipv4.dhcp                            | boolean   | ipv4 address          | true                      | Whether to allocate addresses using DHCP
ipv4.nat                             | boolean   | ipv4 address          | false                     | Whether to NAT (will default to true if unset and a random ipv4.address is generated)
ipv4.nat.address                     | string    | ipv4 address          | -                         | The source address used for outbound traffic from the network (requires uplink `ovn.ingress_mode=routed`)
ipv6.address                         | string    | standard mode         | auto (on create only)     | IPv6 address for the bridge (CIDR notation). Use "none" to turn off IPv6 or "auto" to generate a new random unused subnet
ipv6.dhcp                            | boolean   | ipv6 address          | true                      | Whether to provide additional network configuration over DHCP
ipv6.dhcp.stateful                   | boolean   | ipv6 dhcp             | false                     | Whether to allocate addresses using DHCP
ipv6.nat                             | boolean   | ipv6 address          | false                     | Whether to NAT (will default to true if unset and a random ipv6.address is generated)
ipv6.nat.address                     | string    | ipv6 address          | -                         | The source address used for outbound traffic from the network (requires uplink `ovn.ingress_mode=routed`)
security.acls                        | string    | -                     | -                         | Comma separated list of Network ACLs to apply to NICs connected to this network
security.acls.default.egress.action  | string    | security.acls         | reject                    | Action to use for egress traffic that doesn't match any ACL rule
security.acls.default.egress.logged  | boolean   | security.acls         | false                     | Whether to log egress traffic that doesn't match any ACL rule
security.acls.default.ingress.action | string    | security.acls         | reject                    | Action to use for ingress traffic that doesn't match any ACL rule
security.acls.default.ingress.logged | boolean   | security.acls         | false                     | Whether to log ingress traffic that doesn't match any ACL rule
