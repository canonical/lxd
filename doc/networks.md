# Network configuration

LXD supports the following network types:

 - [bridge](#network-bridge): Creates an L2 bridge for connecting instances to (can provide local DHCP and DNS). This is the default.
 - [macvlan](#network-macvlan): Provides preset configuration to use when connecting instances to a parent macvlan interface.
 - [sriov](#network-sriov): Provides preset configuration to use when connecting instances to a parent SR-IOV interface.

The desired type can be specified using the `--type` argument, e.g.

```bash
lxc network create <name> --type=bridge [options...]
```

If no `--type` argument is specified, the default type of `bridge` is used.

The configuration keys are namespaced with the following namespaces currently supported for all network types:

 - `maas` (MAAS network identification)
 - `user` (free form key/value for user metadata)

## network: bridge

As one of the possible network configuration types under LXD, LXD supports creating and managing network bridges.
LXD bridges can leverage underlying native Linux bridges and Open vSwitch.

Creation and management of LXD bridges is performed via the `lxc network` command.
A bridge created by LXD is by default "managed" which means that LXD also will additionally set up a local `dnsmasq`
DHCP server and if desired also perform NAT for the bridge (this is the default.)

When a bridge is managed by LXD, configuration values under the `bridge` namespace can be used to configure it.

Additionally, LXD can utilize a pre-existing Linux bridge. In this case, the bridge does not need to be created via
`lxc network` and can simply be referenced in an instance or profile device configuration as follows:

```
devices:
  eth0:
     name: eth0
     nictype: bridged
     parent: br0
     type: nic
```

Network configuration properties:

A complete list of configuration settings for LXD networks can be found below.

The following configuration key namespaces are currently supported for bridge networks:

 - `bridge` (L2 interface configuration)
 - `fan` (configuration specific to the Ubuntu FAN overlay)
 - `tunnel` (cross-host tunneling configuration)
 - `ipv4` (L3 IPv4 configuration)
 - `ipv6` (L3 IPv6 configuration)
 - `dns` (DNS server and resolution configuration)
 - `raw` (raw configuration file content)

It is expected that IP addresses and subnets are given using CIDR notation (`1.1.1.1/24` or `fd80:1234::1/64`).

The exception being tunnel local and remote addresses which are just plain addresses (`1.1.1.1` or `fd80:1234::1`).

Key                             | Type      | Condition             | Default                   | Description
:--                             | :--       | :--                   | :--                       | :--
bridge.driver                   | string    | -                     | native                    | Bridge driver ("native" or "openvswitch")
bridge.external\_interfaces     | string    | -                     | -                         | Comma separate list of unconfigured network interfaces to include in the bridge
bridge.hwaddr                   | string    | -                     | -                         | MAC address for the bridge
bridge.mode                     | string    | -                     | standard                  | Bridge operation mode ("standard" or "fan")
bridge.mtu                      | integer   | -                     | 1500                      | Bridge MTU (default varies if tunnel or fan setup)
dns.domain                      | string    | -                     | lxd                       | Domain to advertise to DHCP clients and use for DNS resolution
dns.search                      | string    | -                     | -                         | Full comma eparate domain search list, defaulting to dns.domain
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
maas.subnet.ipv4                | string    | ipv4 address          | -                         | MAAS IPv4 subnet to register instances in (when using `network` property on nic)
maas.subnet.ipv6                | string    | ipv6 address          | -                         | MAAS IPv6 subnet to register instances in (when using `network` property on nic)
raw.dnsmasq                     | string    | -                     | -                         | Additional dnsmasq configuration to append to the configuration file
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

### Integration with systemd-resolved

If the system running LXD uses systemd-resolved to perform DNS
lookups, it's possible to notify resolved of the domain(s) that
LXD is able to resolve.  This requires telling resolved the
specific bridge(s), nameserver address(es), and dns domain(s).

For example, if LXD is using the `lxdbr0` interface, get the
ipv4 address with `lxc network get lxdbr0 ipv4.address` command
(the ipv6 can be used instead or in addition), and the domain
with `lxc network get lxdbr0 dns.domain` (if unset, the domain
is `lxd` as shown in the table above).  Then notify resolved:

```
systemd-resolve --interface lxdbr0 --set-domain '~lxd' --set-dns 1.2.3.4
```

Replace `lxdbr0` with the actual bridge name, and `1.2.3.4` with
the actual address of the nameserver (without the subnet netmask).

Also replace `lxd` with the domain name.  Note the `~` before the
domain name is important; it tells resolved to use this
nameserver to look up only this domain; no matter what your
actual domain name is, you should prefix it with `~`.  Also,
since the shell may expand the `~` character, you may need to
include it in quotes.

In newer releases of systemd, the `systemd-resolve` command has been
deprecated, however it is still provided for backwards compatibility
(as of this writing).  The newer method to notify resolved is using
the `resolvectl` command, which would be done in two steps:

```
resolvectl dns lxdbr0 1.2.3.4
resolvectl domain lxdbr0 '~lxd'
```

This resolved configuration will persist as long as the bridge
exists, so you must repeat this command each reboot and after
LXD is restarted.  Also note this only works if the bridge
`dns.mode` is not `none`.

### IPv6 prefix size
For optimal operation, a prefix size of 64 is preferred.
Larger subnets (prefix smaller than 64) should work properly too but
aren't typically that useful for SLAAC.

Smaller subnets while in theory possible when using stateful DHCPv6 for
IPv6 allocation aren't properly supported by dnsmasq and may be the
source of issue. If you must use one of those, static allocation or
another standalone RA daemon be used.

### Allow DHCP, DNS with Firewalld

In order to allow instances to access the DHCP and DNS server that LXD runs on the host when using firewalld
you need to add the host's bridge interface to the `trusted` zone in firewalld.

To do this permanently (so that it persists after a reboot) run the following command:

```
firewall-cmd --zone=trusted --change-interface=<LXD network name> --permanent
```

E.g. for a bridged network called `lxdbr0` run the command:

```
firewall-cmd --zone=trusted --change-interface=lxdbr0 --permanent
```

This will then allow LXD's own firewall rules to take effect.


### How to let Firewalld control the LXD's iptables rules

When using firewalld and LXD together, iptables rules can overlaps. For example, firewalld could erase LXD iptables rules if it is started after LXD daemon, then LXD container will not be able to do any oubound internet access.
One way to fix it is to delegate to firewalld the LXD's iptables rules and to disable the LXD ones.

First step is to [allow DNS and DHCP](#allow-dhcp-dns-with-firewalld).

Then to tell to LXD totally stop to set iptables rules (because firewalld will do it):
```
lxc network set lxdbr0 ipv4.nat false
lxc network set lxdbr0 ipv6.nat false
lxc network set lxdbr0 ipv6.firewall false
lxc network set lxdbr0 ipv4.firewall false
```

Finally, to enable iptables firewalld's rules for LXD usecase (in this example, we suppose the bridge interface is `lxdbr0` and the associated IP range is `10.0.0.0/24`:
```
firewall-cmd --permanent --direct --add-rule ipv4 filter INPUT 0 -i lxdbr0 -s 10.0.0.0/24 -m comment --comment "generated by firewalld for LXD" -j ACCEPT
firewall-cmd --permanent --direct --add-rule ipv4 filter OUTPUT 0 -o lxdbr0 -d 10.0.0.0/24 -m comment --comment "generated by firewalld for LXD" -j ACCEPT
firewall-cmd --permanent --direct --add-rule ipv4 filter FORWARD 0 -i lxdbr0 -s 10.0.0.0/24 -m comment --comment "generated by firewalld for LXD" -j ACCEPT
firewall-cmd --permanent --direct --add-rule ipv4 nat POSTROUTING 0 -s 10.0.0.0/24 ! -d 10.0.0.0/24 -m comment --comment "generated by firewalld for LXD" -j MASQUERADE
firewall-cmd --reload
```
To check the rules are taken into account by firewalld:
```
firewall-cmd --direct --get-all-rules
```

Warning: what is exposed above is not a fool-proof approach and may end up inadvertently introducing a security risk.

## network: macvlan

The macvlan network type allows one to specify presets to use when connecting instances to a parent interface
using macvlan NICs. This allows the instance NIC itself to simply specify the `network` it is connecting to without
knowing any of the underlying configuration details.

Network configuration properties:

Key                             | Type      | Condition             | Default                   | Description
:--                             | :--       | :--                   | :--                       | :--
parent                          | string    | -                     | -                         | Parent interface to create macvlan NICs on
maas.subnet.ipv4                | string    | ipv4 address          | -                         | MAAS IPv4 subnet to register instances in (when using `network` property on nic)
maas.subnet.ipv6                | string    | ipv6 address          | -                         | MAAS IPv6 subnet to register instances in (when using `network` property on nic)

## network: sriov

The sriov network type allows one to specify presets to use when connecting instances to a parent interface
using sriov NICs. This allows the instance NIC itself to simply specify the `network` it is connecting to without
knowing any of the underlying configuration details.

Network configuration properties:

Key                             | Type      | Condition             | Default                   | Description
:--                             | :--       | :--                   | :--                       | :--
parent                          | string    | -                     | -                         | Parent interface to create sriov NICs on
maas.subnet.ipv4                | string    | ipv4 address          | -                         | MAAS IPv4 subnet to register instances in (when using `network` property on nic)
maas.subnet.ipv6                | string    | ipv6 address          | -                         | MAAS IPv6 subnet to register instances in (when using `network` property on nic)
