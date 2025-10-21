---
relatedlinks: "[Data&#32;centre&#32;networking&#58;&#32;What&#32;is&#32;OVN&#63;](https://ubuntu.com/blog/data-centre-networking-what-is-ovn), [OVN&#32;architectural&#32;overview](https://manpages.ubuntu.com/manpages/noble/man7/ovn-architecture.7.html), [OVN&#32;northbound&#32;database&#32;schema&#32;documentation](https://manpages.ubuntu.com/manpages/noble/en/man5/ovn-nb.5.html), [OVN&#32;southbound&#32;database&#32;schema&#32;documentation](https://manpages.ubuntu.com/manpages/noble/en/man5/ovn-sb.5.html)"
---

(ref-ovn-internals)=
# OVN implementation

Open Virtual Networks (OVN) is an open source Software Defined Network (SDN) solution.
OVN is designed to be incredibly flexible.
This flexibility comes at the cost of complexity.
OVN is not prescriptive about how it should be used.

For LXD, the best way to think of OVN is as a toolkit.
We need to translate networking concepts in LXD to their OVN analogue and instruct OVN directly, at a low level, what to do.

This document outlines LXD's approach to OVN in a basic setup.
It does not yet cover load-balancers, peering, forwards, zones, or ACLs.

For more detailed documentation on OVN itself, please see:

- [Overview of OVN and SDNs](https://ubuntu.com/blog/data-centre-networking-what-is-ovn).
- [OVN architectural overview](https://manpages.ubuntu.com/manpages/noble/man7/ovn-architecture.7.html).
- [OVN northbound database schema documentation](https://manpages.ubuntu.com/manpages/noble/en/man5/ovn-nb.5.html).
- [OVN southbound database schema documentation](https://manpages.ubuntu.com/manpages/noble/en/man5/ovn-sb.5.html).

## OVN concepts
This section outlines the OVN concepts that we use in LXD.
These are usually represented in tables in the OVN northbound database.

(ref-ovn-internals-chassis)=
### Chassis

A chassis is where traffic physically ingresses into or egresses out of the virtual network. In LXD, there will usually be one chassis per cluster member. If LXD is configured to use OVN networking, then all members *can* be used as OVN chassis.

(ref-ovn-internals-chassis-group)=
### Chassis group

A chassis group is an indirection between physical chassis and the virtual networks that use them. Each LXD OVN network has one chassis group. This allows us to, for example, set chassis priority on a per-network basis so that not all ingress/egress occurs on a single cluster member.

If any cluster members are assigned the {ref}`member role <clustering-member-roles>` of `ovn-chassis`, only those members are added to the chassis group. If none are assigned the `ovn-chassis` role, all members are added to the chassis group.

### Open vSwitch (OVS) Bridge
OVS bridges are used to connect virtual networks to physical ones and vice-versa.
If the LXD daemon invokes OVS APIs, that means changes are being applied on the same host machine.

For each LXD cluster member there are two OVS bridges:

- The provider bridge. This is used when connecting the uplink network on the host to the external switch inside each OVN network.
- The integration bridge. This is used when connecting instances to the internal switch inside each OVN network.

### OVN underlay
The OVN underlay is the means by which networks are virtualized across cluster members.
It is a Geneve tunnel which creates a layer 2 overlay network across layer 3 infrastructure.
The OVN underlay is configured and managed by OVN.

### Logical router
A logical router is a virtualized router.
There is one per LXD OVN network.
This handles layer 3 networking and additionally has associated NAT rules and security policies.

### Logical switch
Logical routers cannot be directly connected to OVS bridges; for this, we use a logical switch.
There are two logical switches per LXD OVN network:

- The external switch, which connects via logical switch port to a port on the logical router and to the provider OVS switch.
- The internal switch, which connects via logical switch port to a port on the logical router and to the integration bridge.
  This switch contains DHCP and IP allocation configuration.

### Logical switch/router ports
When you create a logical router or switch in OVN, it doesn't initially have any ports.
You need to create ports and then link them.
For example, the internal logical switch and the logical router for a LXD OVN network are connected by:

1. Adding a logical router port to the logical router.
1. Adding a logical switch port to the internal logical switch.
1. Configuring the internal logical switch port as a router port and setting the logical router name.

Some configuration is applied directly at port level.
For example, in a LXD OVN network, IPv6 router advertisement settings are applied on the logical router port for the internal switch.
This is by design. It allows OVN to push configuration down to the port level so that packets are handled as quickly as possible.

### Port groups
When a LXD OVN network is created, a port group will be created that is specific to that network.
When instances are connected to the network, logical switch ports are created for them on the internal switch.
These logical switch ports are added to the port group for the network.
When a port group is created or updated in the OVN northbound database, the address set table is automatically populated.
Address sets are used for managing access control lists (ACLs).
By creating and maintaining the port group, we can easily select the whole network when managing ACLs.

## OVN Uplink
An OVN network can specify an uplink network.
That uplink network must be a managed network and be of type `physical` or `bridge`.
From these managed network definitions LXD ascertains a `parent` interface to use for the uplink connectivity.

For managed `bridge` networks the interface is the name of the network itself.

For managed `physical` networks it is the per-cluster member value of the `parent` setting.
The `parent` interface itself can have one of three types:

- Linux native bridge.
- OVS Bridge.
- Physical interface (or `bond` or `vlan`).

It is important to note that a `physical` managed network's `parent` interface can be any of these types, and that for a managed `bridge` network the parent interface can be either types of bridges.

### Bridge (OVS)
A user can separately configure a managed bridge network with the `openvswitch` `bridge.driver`.
An OVN network can be created with `network` set to the name of the managed bridge network.
In this case LXD configures a bridge mapping on the OVS bridge to connect the OVN network:

```{figure} /images/ovn/ovn-uplink-bridge-ovs.svg
OVN uplink OVS bridge
```

### Physical
An OVN network can be created with `network` set to a physical network, where the physical network is essentially a database entry in LXD that tells it how to interact with an actual `parent` network device.
In this case, an OVS bridge is created automatically.
A bridge port connects the OVS bridge to the parent.
A bridge mapping is used (as above) to connect the OVS bridge to the OVN network.

```{figure} /images/ovn/ovn-uplink-physical.svg
OVN uplink physical
```

```{note}
When using a physical network as an uplink for OVN, any IP addresses on the parent interface will become defunct.
The parent network must not have any assigned IP addresses.
```

### Bridge (native)
A native Linux bridge can be used.
In this case, we perform the same steps as in the physical network and additionally configure a `veth` pair.
The `veth` pair is used so that the bridge can still be used for other purposes (since the bridge maintains its configuration).
This is handy for development and testing but is not performant and should not be used in production.

```{figure} /images/ovn/ovn-uplink-bridge-native.svg
OVN uplink native bridge
```

## OVN Network
In the simplest case, a LXD OVN network has the below configuration:

```{figure} /images/ovn/ovn-network.svg
:width: 100%

LXD OVN network diagram
```

```{note}
This diagram does not show cross-cluster networks.
This conceptual diagram should look the same on all cluster members.
If the chassis group prioritizes another chassis for the uplink, the traffic is routed through that chassis.
```

## Integration bridge
The cluster setting `network.ovn.integration_bridge` must contain the name of an OVS bridge that is used to connect instances to an OVN network via a NIC device.
This OVS bridge must be pre-configured on all cluster members with the same name.
Connectivity to the integration bridge differs between containers and virtual machines:

- Containers use a `veth` pair (similar to connecting to a native bridge uplink network).

    ```{figure} /images/ovn/ovn-integration-bridge-container.svg
    Integration bridge connectivity with containers
    ```

- Virtual machines use a TAP device (this can be presented to QEMU as a device whereas a `veth` pair cannot).

    ```{figure} /images/ovn/ovn-integration-bridge-vm.svg
    Integration bridge connectivity with virtual machines
    ```
