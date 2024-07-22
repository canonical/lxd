---
discourse: 11033
---

(network-ovn)=
# OVN network

<!-- Include start OVN intro -->
{abbr}`OVN (Open Virtual Network)` is a software-defined networking system that supports virtual network abstraction.
You can use it to build your own private cloud.
See [`www.ovn.org`](https://www.ovn.org/) for more information.
<!-- Include end OVN intro -->

The `ovn` network type allows to create logical networks using the OVN {abbr}`SDN (software-defined networking)`.
This kind of network can be useful for labs and multi-tenant environments where the same logical subnets are used in multiple discrete networks.

A LXD OVN network can be connected to an existing managed {ref}`network-bridge` or {ref}`network-physical` to gain access to the wider network.
By default, all connections from the OVN logical networks are NATed to an IP allocated from the uplink network.

See {ref}`network-ovn-setup` for basic instructions for setting up an OVN network.

% Include content from [network_bridge.md](network_bridge.md)
```{include} network_bridge.md
    :start-after: <!-- Include start MAC identifier note -->
    :end-before: <!-- Include end MAC identifier note -->
```

(network-ovn-architecture)=
## OVN networking architecture

The following figure shows the OVN network traffic flow in a LXD cluster:

```{figure} /images/ovn_networking_1.svg
:width: 100%

OVN networking (one network)
```

The OVN network connects the different cluster members.
Network traffic between the cluster members passes through the NIC for inter-cluster traffic (`eth1` in the figure) and is transmitted through an OVN tunnel.
This traffic between cluster members is referred to as *OVN east/west traffic*.

For outside connectivity, the OVN network requires an uplink network (a {ref}`network-bridge` or a {ref}`network-physical`).
The OVN network uses a virtual router to connect to the uplink network through the NIC for uplink traffic (`eth0` in the figure).
The virtual router is active on only one of the cluster members, and can move to a different member at any time.
Independent of where the router resides, the OVN network is available on all cluster members.

Every instance on any cluster member can connect to the OVN network through its virtual NIC (usually `eth0` for containers and `enp5s0` for virtual machines).
The traffic between the instances and the uplink network is referred to as *OVN north/south traffic*.

The strengths of using OVN become apparent when looking at a networking architecture with more than one OVN network:

```{figure} /images/ovn_networking_2.svg
:width: 100%

OVN networking (two networks)
```

In this case, both depicted OVN networks are completely independent.
Both networks are available on all cluster members (with each virtual router being active on one random cluster member).
Each instance can use either of the networks, and the traffic on either network is completely isolated from the other network.

(network-ovn-options)=
## Configuration options

The following configuration key namespaces are currently supported for the `ovn` network type:

- `bridge` (L2 interface configuration)
- `dns` (DNS server and resolution configuration)
- `ipv4` (L3 IPv4 configuration)
- `ipv6` (L3 IPv6 configuration)
- `security` (network ACL configuration)
- `user` (free-form key/value for user metadata)

```{note}
{{note_ip_addresses_CIDR}}
```

The following configuration options are available for the `ovn` network type:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group network-ovn-network-conf start -->
    :end-before: <!-- config group network-ovn-network-conf end -->
```

(network-ovn-features)=
## Supported features

The following features are supported for the `ovn` network type:

- {ref}`network-acls`
- {ref}`network-forwards`
- {ref}`network-zones`
- {ref}`network-ovn-peers`
- {ref}`network-load-balancers`

```{filtered-toctree}
:maxdepth: 1
:hidden:

:topical:Set up OVN </howto/network_ovn_setup>
:topical:Create routing relationships </howto/network_ovn_peers>
:topical:Configure network load balancers </howto/network_load_balancers>
```
