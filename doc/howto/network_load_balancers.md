---
discourse: lxc:[Network&#32;load-balancers&#32;(OVN)](14317)
---

(network-load-balancers)=
# How to configure network load balancers

```{note}
Network load balancers are currently available for the {ref}`network-ovn`.
```

Network load balancers are similar to forwards in that they allow specific ports on an IP address (external or internal) to be forwarded to specific ports on internal IP addresses in the same network as the load balancer.

The difference between load balancers and forwards is that load balancers can be used to share ingress traffic between multiple internal backend addresses. This feature can be useful if you have limited external IP addresses or want to share a single external address and ports over multiple instances.

A load balancer is made up of:

- A single listen IP address (external or internal).
- One or more named backends consisting of an internal IP and optional port ranges.
- One or more listen port ranges that are configured to forward to one or more named backends.

## Create a network load balancer

Use the following command to create a network load balancer:

```bash
lxc network load-balancer create <network_name> [<listen_address>] [--allocate=ipv{4,6}] [configuration_options...]
```

Example with a specified listen address:

```bash
lxc network load-balancer create my-ovn-network 192.0.2.178
```

Example with an allocated listen address:

```bash
lxc network load-balancer create my-ovn-network --allocate=ipv4
```

Each load balancer is assigned to a network.

Listen addresses are subject to restrictions. If a listen address is not specified, the `--allocate` flag must be provided. See {ref}`network-load-balancers-listen-addresses` for more information about which addresses can be load-balanced, as well as how to use the `--allocate` flag.

### Load balancer properties

Network load balancers have the following properties:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group network-load-balancer-load-balancer-properties start -->
    :end-before: <!-- config group network-load-balancer-load-balancer-properties end -->
```

(network-load-balancers-listen-addresses)=
### Requirements for listen addresses

The following requirements must be met for valid listen addresses:

For external listen IP addresses:

- Allowed listen addresses must be defined in the uplink network's `ipv{n}.routes` settings or the project's {config:option}`project-restricted:restricted.networks.subnets` setting.
   - If you specify a listen address when creating a load balancer, it must be within the range of allowed addresses.
   - If you do not specify a listen address, you must use either `--allocate ipv4` or `--allocate ipv6`. This will allocate a listen address from the range of allowed addresses.
- The listen address must not overlap with a subnet that is in use with another network or entity in that network.

For internal listen IP addresses:

- Allowed listen addresses must not be used by the associated network's gateway, other existing load balancers and network forwards, or instance NICs.

(network-load-balancers-backend-specifications)=
## Configure backends

You can add backend specifications to the network load balancer to define target addresses (and optionally ports).
The backend target address must be within the same subnet as the network associated with the load balancer.

Use the following command to add a backend specification:

```bash
lxc network load-balancer backend add <network_name> <listen_address> <backend_name> <target_address> [<target_ports>]
```

Example:

```bash
lxc network load-balancer backend add my-ovn-network 192.0.2.178 test-backend 10.41.211.5
```

If no target ports are specified when adding the backend:

- The load balancer uses the listen ports defined in the [port specification](#port-properties) associated with that backend, if any.
- If no such listen ports are defined, the backend has no target ports and is inactive. You must either [add a port specification](#port-properties) or [edit the load balancer configuration](#edit-a-network-load-balancer) to include a `target_port` value in the backend specification or a `listen_port` value in the ports specification.

If you want to forward the traffic to different ports, you have two options:

- Specify a single target port to forward traffic from all listen ports to this target port.
- Specify a set of target ports with the same number of ports as the listen ports to forward traffic from the first listen port to the first target port, the second listen port to the second target port, and so on.

### Backend properties

Network load balancer backends have the following properties:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group network-load-balancer-load-balancer-backend-properties start -->
    :end-before: <!-- config group network-load-balancer-load-balancer-backend-properties end -->
```

(network-load-balancers-port-specifications)=
## Configure ports

You can add port specifications to the network load balancer to forward traffic from specific ports on the listen address to specific ports on one or more target backends.

Use the following command to add a port specification:

```bash
lxc network load-balancer port add <network_name> <listen_address> <protocol> <listen_ports> <backend_name>[,<backend_name>...]
```

Example:

```bash
lxc network load-balancer port add my-ovn-network 192.0.2.178 tcp 80 test-backend
```

You can specify a single listen port or a set of ports.
The backend(s) specified must have target port(s) settings compatible with the port's listen port(s) setting.

### Port properties

Network load balancer ports have the following properties:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group network-load-balancer-load-balancer-port-properties start -->
    :end-before: <!-- config group network-load-balancer-load-balancer-port-properties end -->
```

## Edit a network load balancer

Use the following command to edit a network load balancer:

```bash
lxc network load-balancer edit <network_name> <listen_address>
```

This command opens the network load balancer in YAML format for editing.
You can edit the general configuration, as well as the backend and port specifications.

Example load balancer configuration YAML file:

```yaml
listen_address: 192.0.2.178
location: ""
description: ""
config: {}
backends:
- name: test-backend
  description: ""
  target_port: ""
  target_address: 10.41.211.5
ports:
- description: ""
  protocol: tcp
  listen_port: 70,80-90
  target_backend:
  - test-backend
```

## Delete a network load balancer

Use the following command to delete a network load balancer:

```bash
lxc network load-balancer delete <network_name> <listen_address>
```
