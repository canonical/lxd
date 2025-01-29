---
discourse: lxc:11801
---

(network-forwards)=
# How to configure network forwards

```{note}
Network forwards are available for the {ref}`network-ovn` and the {ref}`network-bridge`.
```

```{youtube} https://www.youtube.com/watch?v=B-Uzo9WldMs
```

Network forwards allow an external IP address (or specific ports on it) to be forwarded to an internal IP address (or specific ports on it) in the network that the forward belongs to.

This feature can be useful if you have limited external IP addresses and want to share a single external address between multiple instances.
There are two different ways how you can use network forwards in this case:

- Forward all traffic from the external address to the internal address of one instance.
  This method makes it easy to move the traffic destined for the external address to another instance by simply reconfiguring the network forward.
- Forward traffic from different port numbers of the external address to different instances (and optionally different ports on those instances).
  This method allows to "share" your external IP address and expose more than one instance at a time.

```{tip}
Network forwards are very similar to using a {ref}`proxy device<devices-proxy>` in NAT mode.

The difference is that network forwards are applied on a network level, while a proxy device is added for an instance.
In addition, proxy devices can be used to proxy traffic between different connection types (for example, TCP and Unix sockets).
```

## View network forwards

View a list of forwards configured on a network:

```
lxc network forward list <network_name>
```

Example:

```
lxc network forward list lxdbr0
```

## Create a network forward

(network-forwards-listen-addresses)=
### Requirements for listen addresses

Before you can create a network forward, you must understand the requirements for listen addresses.

For both OVN and bridge networks, the listen addresses must not overlap with any subnet in use by other networks on the host. Otherwise, the listen address requirements differ by network type.

`````{tabs}

````{group-tab} OVN network

For an OVN network, the allowed listen addresses must be defined in at least one of the following configuration options, using [CIDR notation](https://en.wikipedia.org/wiki/Classless_Inter-Domain_Routing):

- {config:option}`network-bridge-network-conf:ipv4.routes` or {config:option}`network-bridge-network-conf:ipv6.routes` in the OVN network's uplink network configuration
- {config:option}`project-restricted:restricted.networks.subnets` in the OVN network's project configuration

````

````{group-tab} Bridge network

A bridge network does not require you to define allowed listen addresses. Use any non-conflicting IP address available on the host.

````

`````

### Create a forward in an OVN network

```{note}
You must configure the {ref}`allowed listen addresses <network-forwards-listen-addresses>` before you can create a forward in an OVN network. 
```

Use the following command to create a forward in an OVN network:

```
lxc network forward create <ovn_network_name> [<listen_address>|--allocate=ipv{4,6}] [target_address=<target_address>] [user.<key>=<value>]
```

- For `<ovn_network_name>`, specify the name of the OVN network on which to create the forward.
- Immediately following the network name, provide only one of the following for the listen address:
   - A listen IP address allowed by the {ref}`network-forwards-listen-addresses` (no port number)
   - The `--allocate=` flag with a value of either `ipv4` or `ipv6` for automatic allocation of an allowed IP address
- Optionally provide a default `target_address` (no port number). Any traffic that does not match a port specification is forwarded to this address. This must be an IP range within the OVN network's subnet.
- Optionally provide custom user.* keys to be stored in the network forward's configuration.

This example shows how to create a network forward on a network named `ovn1` with an allocated listen address and no default target address:

```
lxd network forward create ovn1 --allocate=ipv4 
```

This example shows how to create a network forward on a network named `ovn1` with a specific listen address and a target address:

```
lxd network forward create ovn1 192.0.2.1 target_address=10.41.211.2
```

```{note}
The IP addresses shown in the example above are only examples. It is up to you to choose the allowed and available addresses on your setup.  
```

### Create a forward in a bridge network

Use the following command to create a forward in a bridge network:

```
lxc network forward create <bridge_network_name> <listen_address> [target_address=<target_address>] [user.<key>=<value>] 
```

- For `<bridge_network_name>`, specify the name of the bridge network on which to create the forward.
- Immediately following the network name, provide a listen IP address allowed by the {ref}`network-forwards-listen-addresses` (no port number).
- Optionally provide a default `target_address` (no port number). Any traffic that does not match a port specification is forwarded to this address. This must be an IP address within the bridge network's subnet.
- Optionally provide custom user.* keys to be stored in the network forward's configuration.
- You cannot use the `--allocate` flag with bridge networks.

This example shows how to create a network forward on a network named `ovn1` with a specific listen address and a target address:

```
lxd network forward create bridge1 192.0.2.1 target_address=10.41.211.2
```

```{note}
The IP addresses shown in the example above are only examples. It is up to you to choose the allowed and available addresses on your setup.  
```

### Forward properties

Network forwards have the following properties:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group network-forward-forward-properties start -->
    :end-before: <!-- config group network-forward-forward-properties end -->
```

(network-forwards-port-specifications)=
## Configure ports

You can add port specifications to the network forward to forward traffic from specific ports on the listen address to specific ports on the target address.
This target address must be different from the default target address.
It must be within the same subnet as the network that the forward is associated to.

Use the following command to add a port specification:

```bash
lxc network forward port add <network_name> <listen_address> <protocol> <listen_ports> <target_address> [<target_ports>]
```

You can specify a single listen port or a set of ports.
If you want to forward the traffic to different ports, you have two options:

- Specify a single target port to forward traffic from all listen ports to this target port.
- Specify a set of target ports with the same number of ports as the listen ports to forward traffic from the first listen port to the first target port, the second listen port to the second target port, and so on.

### Port properties

Network forward ports have the following properties:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group network-forward-port-properties start -->
    :end-before: <!-- config group network-forward-port-properties end -->
```

## Edit a network forward

Use the following command to edit a network forward:

```bash
lxc network forward edit <network_name> <listen_address>
```

This command opens the network forward in YAML format for editing.
You can edit both the general configuration and the port specifications.

## Delete a network forward

Use the following command to delete a network forward:

```bash
lxc network forward delete <network_name> <listen_address>
```
