# Network configuration

The key/value configuration is namespaced with the following namespaces
currently supported:

 - `bridge` (L2 interface configuration)
 - `fan` (configuration specific to the Ubuntu FAN overlay)
 - `tunnel` (cross-host tunneling configuration)
 - `ipv4` (L3 IPv4 configuration)
 - `ipv6` (L3 IPv6 configuration)
 - `dns` (DNS server and resolution configuration)
 - `raw` (raw configuration file content)
 - `user` (free form key/value for user metadata)

## Bridges

As one of the possible network configuration types under LXD,
LXD supports creating and managing network bridges. LXD bridges 
can leverage underlying native Linux bridges and Open vSwitch. 

Creation and management of LXD bridges is performed via the `lxc network`
command. A bridge created by LXD is by default "managed" which 
means that LXD also will additionally set up a local `dnsmasq` 
DHCP server and if desired also perform NAT for the bridge (this 
is the default.)

When a bridge is managed by LXD, configuration values
under the `bridge` namespace can be used to configure it.

Additionally, LXD can utilize a pre-existing Linux
bridge. In this case, the bridge does not need to be created via
`lxd network` and can simply be referenced in a container or
profile device configuration as follows:

```
devices:
  eth0:
     name: eth0
     nictype: bridged
     parent: br0
     type: nic
```

## Configuration Settings

A complete list of configuration settings for LXD networks can
be found below.

It is expected that IP addresses and subnets are given using CIDR 
notation (`1.1.1.1/24` or `fd80:1234::1/64`). The exception being 
tunnel local and remote addresses which are just plain addresses 
(`1.1.1.1` or `fd80:1234::1`).

Key                             | Type      | Condition             | Default                   | Description
:--                             | :--       | :--                   | :--                       | :--
bridge.driver                   | string    | -                     | native                    | Bridge driver ("native" or "openvswitch")
bridge.external\_interfaces     | string    | -                     | -                         | Comma separate list of unconfigured network interfaces to include in the bridge
bridge.hwaddr                   | string    | -                     | -                         | MAC address for the bridge
bridge.mode                     | string    | -                     | standard                  | Bridge operation mode ("standard" or "fan")
bridge.mtu                      | integer   | -                     | 1500                      | Bridge MTU (default varies if tunnel or fan setup)
dns.domain                      | string    | -                     | lxd                       | Domain to advertise to DHCP clients and use for DNS resolution
dns.mode                        | string    | -                     | managed                   | DNS registration mode ("none" for no DNS record, "managed" for LXD generated static records or "dynamic" for client generated records)
fan.overlay\_subnet             | string    | fan mode              | 240.0.0.0/8               | Subnet to use as the overlay for the FAN (CIDR notation)
fan.type                        | string    | fan mode              | vxlan                     | The tunneling type for the FAN ("vxlan" or "ipip")
fan.underlay\_subnet            | string    | fan mode              | default gateway subnet    | Subnet to use as the underlay for the FAN (CIDR notation)
ipv4.address                    | string    | standard mode         | random unused subnet      | IPv4 address for the bridge (CIDR notation). Use "none" to turn off IPv4 or "auto" to generate a new one
ipv4.dhcp                       | boolean   | ipv4 address          | true                      | Whether to allocate addresses using DHCP
ipv4.dhcp.expiry                | string    | ipv4 dhcp             | 1h                        | When to expire DHCP leases
ipv4.dhcp.gateway               | string    | ipv4 dhcp             | ipv4.address              | Address of the gateway for the subnet
ipv4.dhcp.ranges                | string    | ipv4 dhcp             | all addresses             | Comma separated list of IP ranges to use for DHCP (FIRST-LAST format)
ipv4.firewall                   | boolean   | ipv4 address          | true                      | Whether to generate filtering firewall rules for this network
ipv4.nat                        | boolean   | ipv4 address          | false                     | Whether to NAT (will default to true if unset and a random ipv4.address is generated)
ipv4.nat.order                  | string    | ipv4 address          | before                    | Whether to add the required NAT rules before or after any pre-existing rules
ipv4.nat.address                | string    | ipv4 address          | -                         | The source address used for outbound traffic from the bridge
ipv4.routes                     | string    | ipv4 address          | -                         | Comma separated list of additional IPv4 CIDR subnets to route to the bridge
ipv4.routing                    | boolean   | ipv4 address          | true                      | Whether to route traffic in and out of the bridge
ipv6.address                    | string    | standard mode         | random unused subnet      | IPv6 address for the bridge (CIDR notation). Use "none" to turn off IPv6 or "auto" to generate a new one
ipv6.dhcp                       | boolean   | ipv6 address          | true                      | Whether to provide additional network configuration over DHCP
ipv6.dhcp.expiry                | string    | ipv6 dhcp             | 1h                        | When to expire DHCP leases
ipv6.dhcp.ranges                | string    | ipv6 stateful dhcp    | all addresses             | Comma separated list of IPv6 ranges to use for DHCP (FIRST-LAST format)
ipv6.dhcp.stateful              | boolean   | ipv6 dhcp             | false                     | Whether to allocate addresses using DHCP
ipv6.firewall                   | boolean   | ipv6 address          | true                      | Whether to generate filtering firewall rules for this network
ipv6.nat                        | boolean   | ipv6 address          | false                     | Whether to NAT (will default to true if unset and a random ipv6.address is generated)
ipv6.nat.order                  | string    | ipv6 address          | before                    | Whether to add the required NAT rules before or after any pre-existing rules
ipv6.nat.address                | string    | ipv6 address          | -                         | The source address used for outbound traffic from the bridge
ipv6.routes                     | string    | ipv6 address          | -                         | Comma separated list of additional IPv6 CIDR subnets to route to the bridge
ipv6.routing                    | boolean   | ipv6 address          | true                      | Whether to route traffic in and out of the bridge
raw.dnsmasq                     | string    | -                     | -                         | Additional dnsmasq configuration to append to the configuration file.
tunnel.NAME.group               | string    | vxlan                 | 239.0.0.1                 | Multicast address for vxlan (used if local and remote aren't set)
tunnel.NAME.id                  | integer   | vxlan                 | 0                         | Specific tunnel ID to use for the vxlan tunnel
tunnel.NAME.interface           | string    | vxlan                 | -                         | Specific host interface to use for the tunnel
tunnel.NAME.local               | string    | gre or vxlan          | -                         | Local address for the tunnel (not necessary for multicast vxlan)
tunnel.NAME.port                | integer   | vxlan                 | 0                         | Specific port to use for the vxlan tunnel
tunnel.NAME.protocol            | string    | standard mode         | -                         | Tunneling protocol ("vxlan" or "gre")
tunnel.NAME.remote              | string    | gre or vxlan          | -                         | Remote address for the tunnel (not necessary for multicast vxlan)
tunnel.NAME.ttl                 | integer   | vxlan                 | 1                         | Specific TTL to use for multicast routing topologies


Those keys can be set using the lxc tool with:

```bash
lxc network set <network> <key> <value>
```
