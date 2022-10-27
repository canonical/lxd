(devices-nic)=
# Type: `nic`

LXD supports several different kinds of network devices (referred to as Network Interface Controller or NIC).

When adding a network device to an instance, there are two ways to specify the type of device you want to add;
either by specifying the `nictype` property or using the `network` property.

## Specifying a NIC using the `network` property

When specifying the `network` property, the NIC is linked to an existing managed network and the `nictype` is
automatically detected based on the network's type.

Some of the NICs properties are inherited from the network rather than being customizable for each NIC.

These are detailed in the "Managed" column in the NIC specific sections below.

## NICs Available

See the settings for the NIC below for details about which properties are available.

The following NICs can be specified using the `nictype` or `network` properties:

- [`bridged`](#nic-bridged): Uses an existing bridge on the host and creates a virtual device pair to connect the host bridge to the instance.
- [`macvlan`](#nic-macvlan): Sets up a new network device based on an existing one but using a different MAC address.
- [`sriov`](#nic-sriov): Passes a virtual function of an SR-IOV enabled physical network device into the instance.

The following NICs can be specified using only the `network` property:

- [`ovn`](#nic-ovn): Uses an existing OVN network and creates a virtual device pair to connect the instance to it.

The following NICs can be specified using only the `nictype` property:

- [`physical`](#nic-physical): Straight physical device pass-through from the host. The targeted device will vanish from the host and appear in the instance.
- [`ipvlan`](#nic-ipvlan): Sets up a new network device based on an existing one using the same MAC address but a different IP.
- [`p2p`](#nic-p2p): Creates a virtual device pair, putting one side in the instance and leaving the other side on the host.
- [`routed`](#nic-routed): Creates a virtual device pair to connect the host to the instance and sets up static routes and proxy ARP/NDP entries to allow the instance to join the network of a designated parent interface.

(instance_device_type_nic_bridged)=
## `nic`: `bridged`

Supported instance types: container, VM

Selected using: `nictype`, `network`

Uses an existing bridge on the host and creates a virtual device pair to connect the host bridge to the instance.

Device configuration properties:

Key                      | Type    | Default           | Required | Managed | Description
:--                      | :--     | :--               | :--      | :--     | :--
`parent`                 | string  | -                 | yes      | yes     | The name of the host device
`network`                | string  | -                 | yes      | no      | The LXD network to link device to (instead of parent)
`name`                   | string  | kernel assigned   | no       | no      | The name of the interface inside the instance
`mtu`                    | integer | parent MTU        | no       | yes     | The MTU of the new interface
`hwaddr`                 | string  | randomly assigned | no       | no      | The MAC address of the new interface
`host_name`              | string  | randomly assigned | no       | no      | The name of the interface inside the host
`limits.ingress`         | string  | -                 | no       | no      | I/O limit in bit/s for incoming traffic (various suffixes supported, see {ref}`instances-limit-units`)
`limits.egress`          | string  | -                 | no       | no      | I/O limit in bit/s for outgoing traffic (various suffixes supported, see {ref}`instances-limit-units`)
`limits.max`             | string  | -                 | no       | no      | Same as modifying both `limits.ingress` and `limits.egress`
`ipv4.address`           | string  | -                 | no       | no      | An IPv4 address to assign to the instance through DHCP (Can be `none` to restrict all IPv4 traffic when `security.ipv4_filtering` is set)
`ipv6.address`           | string  | -                 | no       | no      | An IPv6 address to assign to the instance through DHCP (Can be `none` to restrict all IPv6 traffic when `security.ipv6_filtering` is set)
`ipv4.routes`            | string  | -                 | no       | no      | Comma-delimited list of IPv4 static routes to add on host to NIC
`ipv6.routes`            | string  | -                 | no       | no      | Comma-delimited list of IPv6 static routes to add on host to NIC
`ipv4.routes.external`   | string  | -                 | no       | no      | Comma-delimited list of IPv4 static routes to route to the NIC and publish on uplink network (BGP)
`ipv6.routes.external`   | string  | -                 | no       | no      | Comma-delimited list of IPv6 static routes to route to the NIC and publish on uplink network (BGP)
`security.mac_filtering` | bool    | `false`           | no       | no      | Prevent the instance from spoofing another instance's MAC address
`security.ipv4_filtering`| bool    | `false`           | no       | no      | Prevent the instance from spoofing another instance's IPv4 address (enables `mac_filtering`)
`security.ipv6_filtering`| bool    | `false`           | no       | no      | Prevent the instance from spoofing another instance's IPv6 address (enables `mac_filtering`)
`maas.subnet.ipv4`       | string  | -                 | no       | yes     | MAAS IPv4 subnet to register the instance in
`maas.subnet.ipv6`       | string  | -                 | no       | yes     | MAAS IPv6 subnet to register the instance in
`boot.priority`          | integer | -                 | no       | no      | Boot priority for VMs (higher boots first)
`vlan`                   | integer | -                 | no       | no      | The VLAN ID to use for non-tagged traffic (Can be `none` to remove port from default VLAN)
`vlan.tagged`            | integer | -                 | no       | no      | Comma-delimited list of VLAN IDs or VLAN ranges to join for tagged traffic
`security.port_isolation`| bool    | `false`           | no       | no      | Prevent the NIC from communicating with other NICs in the network that have port isolation enabled

## `nic`: `macvlan`

Supported instance types: container, VM

Selected using: `nictype`, `network`

Sets up a new network device based on an existing one but using a different MAC address.

Device configuration properties:

Key                     | Type    | Default           | Required | Managed | Description
:--                     | :--     | :--               | :--      | :--     | :--
`parent`                | string  | -                 | yes      | yes     | The name of the host device
`network`               | string  | -                 | yes      | no      | The LXD network to link device to (instead of parent)
`name`                  | string  | kernel assigned   | no       | no      | The name of the interface inside the instance
`mtu`                   | integer | parent MTU        | no       | yes     | The MTU of the new interface
`hwaddr`                | string  | randomly assigned | no       | no      | The MAC address of the new interface
`vlan`                  | integer | -                 | no       | no      | The VLAN ID to attach to
`gvrp`                  | bool    | `false`           | no       | no      | Register VLAN using GARP VLAN Registration Protocol
`maas.subnet.ipv4`      | string  | -                 | no       | yes     | MAAS IPv4 subnet to register the instance in
`maas.subnet.ipv6`      | string  | -                 | no       | yes     | MAAS IPv6 subnet to register the instance in
`boot.priority`         | integer | -                 | no       | no      | Boot priority for VMs (higher boots first)

## `nic`: `sriov`

Supported instance types: container, VM

Selected using: `nictype`, `network`

Passes a virtual function of an SR-IOV enabled physical network device into the instance.

Device configuration properties:

Key                     | Type    | Default           | Required | Managed | Description
:--                     | :--     | :--               | :--      | :--     | :--
`parent`                | string  | -                 | yes      | yes     | The name of the host device
`network`               | string  | -                 | yes      | no      | The LXD network to link device to (instead of parent)
`name`                  | string  | kernel assigned   | no       | no      | The name of the interface inside the instance
`mtu`                   | integer | kernel assigned   | no       | yes     | The MTU of the new interface
`hwaddr`                | string  | randomly assigned | no       | no      | The MAC address of the new interface
`security.mac_filtering`| bool    | `false`           | no       | no      | Prevent the instance from spoofing another instance's MAC address
`vlan`                  | integer | -                 | no       | no      | The VLAN ID to attach to
`maas.subnet.ipv4`      | string  | -                 | no       | yes     | MAAS IPv4 subnet to register the instance in
`maas.subnet.ipv6`      | string  | -                 | no       | yes     | MAAS IPv6 subnet to register the instance in
`boot.priority`         | integer | -                 | no       | no      | Boot priority for VMs (higher boots first)

(instance_device_type_nic_ovn)=
## `nic`: `ovn`

Supported instance types: container, VM

Selected using: `network`

Uses an existing OVN network and creates a virtual device pair to connect the instance to it.

Device configuration properties:

Key                                  | Type    | Default           | Required | Managed | Description
:--                                  | :--     | :--               | :--      | :--     | :--
`network`                            | string  | -                 | yes      | yes     | The LXD network to link device to
`acceleration`                       | string  | `none`            | no       | no      | Enable hardware offloading. Either `none` or `sriov` (see SR-IOV hardware acceleration below)
`name`                               | string  | kernel assigned   | no       | no      | The name of the interface inside the instance
`host_name`                          | string  | randomly assigned | no       | no      | The name of the interface inside the host
`hwaddr`                             | string  | randomly assigned | no       | no      | The MAC address of the new interface
`ipv4.address`                       | string  | -                 | no       | no      | An IPv4 address to assign to the instance through DHCP
`ipv6.address`                       | string  | -                 | no       | no      | An IPv6 address to assign to the instance through DHCP
`ipv4.routes`                        | string  | -                 | no       | no      | Comma-delimited list of IPv4 static routes to route to the NIC
`ipv6.routes`                        | string  | -                 | no       | no      | Comma-delimited list of IPv6 static routes to route to the NIC
`ipv4.routes.external`               | string  | -                 | no       | no      | Comma-delimited list of IPv4 static routes to route to the NIC and publish on uplink network
`ipv6.routes.external`               | string  | -                 | no       | no      | Comma-delimited list of IPv6 static routes to route to the NIC and publish on uplink network
`boot.priority`                      | integer | -                 | no       | no      | Boot priority for VMs (higher boots first)
`security.acls`                      | string  | -                 | no       | no      | Comma-separated list of Network ACLs to apply
`security.acls.default.ingress.action`| string | `reject`          | no       | no      | Action to use for ingress traffic that doesn't match any ACL rule
`security.acls.default.egress.action` | string | `reject`          | no       | no      | Action to use for egress traffic that doesn't match any ACL rule
`security.acls.default.ingress.logged`| bool   | `false`           | no       | no      | Whether to log ingress traffic that doesn't match any ACL rule
`security.acls.default.egress.logged` | bool   | `false`           | no       | no      | Whether to log egress traffic that doesn't match any ACL rule

SR-IOV hardware acceleration:

In order to use `acceleration=sriov` you need to have a compatible SR-IOV switchdev capable physical NIC in your LXD
host. LXD assumes that the physical NIC (PF) will be configured in switchdev mode and will be connected to the OVN
integration OVS bridge and that it will have one or more virtual functions (VFs) active.

The basic prerequisite setup steps to achieve this are:

PF and VF setup:

Activate some VFs on PF (in this case called `enp9s0f0np0` with a PCI address of `0000:09:00.0`) and unbind them.
Then enable `switchdev` mode and `hw-tc-offload` on the the PF.
Finally rebind the VFs.

```
echo 4 > /sys/bus/pci/devices/0000:09:00.0/sriov_numvfs
for i in $(lspci -nnn | grep "Virtual Function" | cut -d' ' -f1); do echo 0000:$i > /sys/bus/pci/drivers/mlx5_core/unbind; done
devlink dev eswitch set pci/0000:09:00.0 mode switchdev
ethtool -K enp9s0f0np0 hw-tc-offload on
for i in $(lspci -nnn | grep "Virtual Function" | cut -d' ' -f1); do echo 0000:$i > /sys/bus/pci/drivers/mlx5_core/bind; done
```

OVS setup:

Enable hardware offload and add the PF NIC to the integration bridge (normally called `br-int`):

```
ovs-vsctl set open_vswitch . other_config:hw-offload=true
systemctl restart openvswitch-switch
ovs-vsctl add-port br-int enp9s0f0np0
ip link set enp9s0f0np0 up
```

## `nic`: `physical`

Supported instance types: container, VM

Selected using: `nictype`

Straight physical device pass-through from the host. The targeted device will vanish from the host and appear in the instance.

Device configuration properties:

Key                     | Type    | Default           | Required | Description
:--                     | :--     | :--               | :--      | :--
`parent`                | string  | -                 | yes      | The name of the host device
`name`                  | string  | kernel assigned   | no       | The name of the interface inside the instance
`mtu`                   | integer | parent MTU        | no       | The MTU of the new interface
`hwaddr`                | string  | randomly assigned | no       | The MAC address of the new interface
`vlan`                  | integer | -                 | no       | The VLAN ID to attach to
`gvrp`                  | bool    | `false`           | no       | Register VLAN using GARP VLAN Registration Protocol
`maas.subnet.ipv4`      | string  | -                 | no       | MAAS IPv4 subnet to register the instance in
`maas.subnet.ipv6`      | string  | -                 | no       | MAAS IPv6 subnet to register the instance in
`boot.priority`         | integer | -                 | no       | Boot priority for VMs (higher boots first)

## `nic`: `ipvlan`

Supported instance types: container

Selected using: `nictype`

Sets up a new network device based on an existing one using the same MAC address but a different IP.

LXD currently supports IPVLAN in L2 and L3S mode.

In this mode, the gateway is automatically set by LXD, however IP addresses must be manually specified using either one or both of `ipv4.address` and `ipv6.address` settings before instance is started.

For DNS, the name servers need to be configured inside the instance, as these will not automatically be set.

It requires the following `sysctls` to be set:

If using IPv4 addresses:

```
net.ipv4.conf.<parent>.forwarding=1
```

If using IPv6 addresses:

```
net.ipv6.conf.<parent>.forwarding=1
net.ipv6.conf.<parent>.proxy_ndp=1
```

Device configuration properties:

Key                     | Type    | Default            | Required | Description
:--                     | :--     | :--                | :--      | :--
`parent`                | string  | -                  | yes      | The name of the host device
`name`                  | string  | kernel assigned    | no       | The name of the interface inside the instance
`mtu`                   | integer | parent MTU         | no       | The MTU of the new interface
`mode`                  | string  | `l3s`              | no       | The IPVLAN mode (either `l2` or `l3s`)
`hwaddr`                | string  | randomly assigned  | no       | The MAC address of the new interface
`ipv4.address`          | string  | -                  | no       | Comma-delimited list of IPv4 static addresses to add to the instance. In `l2` mode these can be specified as CIDR values or singular addresses (if singular a subnet of /24 is used).
`ipv4.gateway`          | string  | `auto`             | no       | In `l3s` mode, whether to add an automatic default IPv4 gateway, can be `auto` or `none`. In `l2` mode specifies the IPv4 address of the gateway.
`ipv4.host_table`       | integer | -                  | no       | The custom policy routing table ID to add IPv4 static routes to (in addition to main routing table).
`ipv6.address`          | string  | -                  | no       | Comma-delimited list of IPv6 static addresses to add to the instance. In `l2` mode these can be specified as CIDR values or singular addresses (if singular a subnet of /64 is used).
`ipv6.gateway`          | string  | `auto` (`l3s`), - (`l2`) | no       | In `l3s` mode, whether to add an automatic default IPv6 gateway, can be `auto` or `none`. In `l2` mode specifies the IPv6 address of the gateway.
`ipv6.host_table`       | integer | -                  | no       | The custom policy routing table ID to add IPv6 static routes to (in addition to main routing table).
`vlan`                  | integer | -                  | no       | The VLAN ID to attach to
`gvrp`                  | bool    | `false`            | no       | Register VLAN using GARP VLAN Registration Protocol

## `nic`: `p2p`

Supported instance types: container, VM

Selected using: `nictype`

Creates a virtual device pair, putting one side in the instance and leaving the other side on the host.

Device configuration properties:

Key                     | Type    | Default           | Required | Description
:--                     | :--     | :--               | :--      | :--
`name`                  | string  | kernel assigned   | no       | The name of the interface inside the instance
`mtu`                   | integer | kernel assigned   | no       | The MTU of the new interface
`hwaddr`                | string  | randomly assigned | no       | The MAC address of the new interface
`host_name`             | string  | randomly assigned | no       | The name of the interface inside the host
`limits.ingress`        | string  | -                 | no       | I/O limit in bit/s for incoming traffic (various suffixes supported, see {ref}`instances-limit-units`)
`limits.egress`         | string  | -                 | no       | I/O limit in bit/s for outgoing traffic (various suffixes supported, see {ref}`instances-limit-units`)
`limits.max`            | string  | -                 | no       | Same as modifying both `limits.ingress` and `limits.egress`
`ipv4.routes`           | string  | -                 | no       | Comma-delimited list of IPv4 static routes to add on host to NIC
`ipv6.routes`           | string  | -                 | no       | Comma-delimited list of IPv6 static routes to add on host to NIC
`boot.priority`         | integer | -                 | no       | Boot priority for VMs (higher boots first)

## `nic`: `routed`

Supported instance types: container, VM

Selected using: `nictype`

This NIC type is similar in operation to IPVLAN, in that it allows an instance to join an external network without needing to configure a bridge and shares the host's MAC address.

However it differs from IPVLAN because it does not need IPVLAN support in the kernel and the host and instance can communicate with each other.

It will also respect `netfilter` rules on the host and will use the host's routing table to route packets which can be useful if the host is connected to multiple networks.

IP addresses must be manually specified using either one or both of `ipv4.address` and `ipv6.address` settings before the instance is started.

For containers it uses a virtual Ethernet device pair, and for VMs it uses a TAP device. It then configures the following link-local gateway IPs on the host end which are then set as the default gateways in the instance:

    169.254.0.1
    fe80::1

For containers these are automatically set as default gateways on the instance NIC interface.
But for VMs the IP addresses and gateways will need to be configured manually or via a mechanism like cloud-init.

Note also that if your container image is configured to perform DHCP on the interface it will likely remove the
automatically added configuration, and will need to be configured manually or via a mechanism like cloud-init.

It then configures static routes on the host pointing to the instance's `veth` interface for all of the instance's IPs.

This NIC can operate with and without a `parent` network interface set.

With the `parent` network interface set proxy ARP/NDP entries of the instance's IPs are added to the parent interface allowing the instance to join the parent interface's network at layer 2.

For DNS, the name servers need to be configured inside the instance, as these will not automatically be set.

It requires the following `sysctls` to be set:

If using IPv4 addresses:

```
net.ipv4.conf.<parent>.forwarding=1
```

If using IPv6 addresses:

```
net.ipv6.conf.all.forwarding=1
net.ipv6.conf.<parent>.forwarding=1
net.ipv6.conf.all.proxy_ndp=1
net.ipv6.conf.<parent>.proxy_ndp=1
```

Each NIC device can have multiple IP addresses added to them. However it may be desirable to utilize multiple `routed` NIC interfaces.
In these cases one should set the `ipv4.gateway` and `ipv6.gateway` values to `none` on any subsequent interfaces to avoid default gateway conflicts.
It may also be useful to specify a different host-side address for these subsequent interfaces using `ipv4.host_address` and `ipv6.host_address` respectively.

Device configuration properties:

Key                     | Type    | Default           | Required | Description
:--                     | :--     | :--               | :--      | :--
`parent`                | string  | -                 | no       | The name of the host device to join the instance to
`name`                  | string  | kernel assigned   | no       | The name of the interface inside the instance
`host_name`             | string  | randomly assigned | no       | The name of the interface inside the host
`mtu`                   | integer | parent MTU        | no       | The MTU of the new interface
`hwaddr`                | string  | randomly assigned | no       | The MAC address of the new interface
`limits.ingress`        | string  | -                 | no       | I/O limit in bit/s for incoming traffic (various suffixes supported, see {ref}`instances-limit-units`)
`limits.egress`         | string  | -                 | no       | I/O limit in bit/s for outgoing traffic (various suffixes supported, see {ref}`instances-limit-units`)
`limits.max`            | string  | -                 | no       | Same as modifying both `limits.ingress` and `limits.egress`
`ipv4.address`          | string  | -                 | no       | Comma-delimited list of IPv4 static addresses to add to the instance
`ipv4.routes`           | string  | -                 | no       | Comma-delimited list of IPv4 static routes to add on host to NIC (without L2 ARP/NDP proxy)
`ipv4.gateway`          | string  | `auto`            | no       | Whether to add an automatic default IPv4 gateway, can be `auto` or `none`
`ipv4.host_address`     | string  | `169.254.0.1`     | no       | The IPv4 address to add to the host-side `veth` interface
`ipv4.host_table`       | integer | -                 | no       | The custom policy routing table ID to add IPv4 static routes to (in addition to main routing table)
`ipv4.neighbor_probe`   | bool    | `true`            | no       | Whether to probe the parent network for IP address availability.
`ipv6.address`          | string  | -                 | no       | Comma-delimited list of IPv6 static addresses to add to the instance
`ipv6.routes`           | string  | -                 | no       | Comma-delimited list of IPv6 static routes to add on host to NIC (without L2 ARP/NDP proxy)
`ipv6.gateway`          | string  | `auto`            | no       | Whether to add an automatic default IPv6 gateway, can be `auto` or `none`
`ipv6.host_address`     | string  | `fe80::1`         | no       | The IPv6 address to add to the host-side `veth` interface
`ipv6.host_table`       | integer | -                 | no       | The custom policy routing table ID to add IPv6 static routes to (in addition to main routing table)
`ipv6.neighbor_probe`   | bool    | `true`            | no       | Whether to probe the parent network for IP address availability.
`vlan`                  | integer | -                 | no       | The VLAN ID to attach to
`gvrp`                  | bool    | `false`           | no       | Register VLAN using GARP VLAN Registration Protocol

## `bridged`, `macvlan` or `ipvlan` for connection to physical network

The `bridged`, `macvlan` and `ipvlan` interface types can be used to connect to an existing physical network.

`macvlan` effectively lets you fork your physical NIC, getting a second interface that's then used by the instance.
This saves you from creating a bridge device and virtual Ethernet device pairs and usually offers better performance than a bridge.

The downside to this is that `macvlan` devices while able to communicate between themselves and to the outside, aren't able to talk to their parent device.
This means that you can't use `macvlan` if you ever need your instances to talk to the host itself.

In such case, a `bridge` device is preferable. A bridge will also let you use MAC filtering and I/O limits which cannot be applied to a macvlan device.

`ipvlan` is similar to `macvlan`, with the difference being that the forked device has IPs statically assigned to it and inherits the parent's MAC address on the network.

## SR-IOV

The `sriov` interface type supports SR-IOV enabled network devices.
These devices associate a set of virtual functions (VFs) with the single physical function (PF) of the network device.
PFs are standard PCIe functions. VFs on the other hand are very lightweight PCIe functions that are optimized for data movement.
They come with a limited set of configuration capabilities to prevent changing properties of the PF.
Given that VFs appear as regular PCIe devices to the system they can be passed to instances just like a regular physical device.
The `sriov` interface type expects to be passed the name of an SR-IOV enabled network device on the system via the `parent` property.
LXD will then check for any available VFs on the system. By default LXD will allocate the first free VF it finds.
If it detects that either none are enabled or all currently enabled VFs are in use it will bump the number of supported VFs to the maximum value and use the first free VF.
If all possible VFs are in use or the kernel or card doesn't support incrementing the number of VFs LXD will return an error.

To create a `sriov` network device use:

```
lxc config device add <instance> <device-name> nic nictype=sriov parent=<sriov-enabled-device>
```

To tell LXD to use a specific unused VF add the `host_name` property and pass
it the name of the enabled VF.

## MAAS integration

If you're using MAAS to manage the physical network under your LXD host
and want to attach your instances directly to a MAAS managed network,
LXD can be configured to interact with MAAS so that it can track your
instances.

At the daemon level, you must configure `maas.api.url` and
`maas.api.key`, then set the `maas.subnet.ipv4` and/or
`maas.subnet.ipv6` keys on the instance or profile's `nic` entry.

This will have LXD register all your instances with MAAS, giving them
proper DHCP leases and DNS records.

If you set the `ipv4.address` or `ipv6.address` keys on the NIC, then
those will be registered as static assignments in MAAS too.
