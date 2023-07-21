(network-ipam)=
# How to display IPAM information of a LXD deployment

{abbr}`IPAM (IP Address Management)` is a method used to plan, track, and manage the information associated with a computer network's IP address space. In essence, it's a way of organizing, monitoring, and manipulating the IP space in a network.

Checking the IPAM information for your LXD setup can help you debug networking issues. You can see which IP addresses are used for instances, network interfaces, forwards, and load balancers and use this information to track down where traffic is lost.

To display IPAM information, enter the following command:

```bash
lxc network list-allocations
```

By default, this command shows the IPAM information for the `default` project. You can select a different project with the `--project` flag, or specify `--all-projects` to display the information for all projects.

The resulting output will look something like this:

```
+----------------------+-------------------------------------------+----------+------+-------------------+
|       USED BY        |                  ADDRESS                  |   TYPE   | NAT  | HARDWARE ADDRESS  |
+----------------------+-------------------------------------------+----------+------+-------------------+
| /1.0/networks/lxdbr0 | 10.6.105.1/24                             | network  | true |                   |
+----------------------+-------------------------------------------+----------+------+-------------------+
| /1.0/networks/lxdbr0 | fd42:3cce:990:a1fd::1/64                  | network  | true |                   |
+----------------------+-------------------------------------------+----------+------+-------------------+
| /1.0/instances/u1    | fd42:3cce:990:a1fd:216:3eff:fe04:f095/128 | instance | true | 00:16:3e:04:f0:95 |
+----------------------+-------------------------------------------+----------+------+-------------------+
| /1.0/instances/u1    | 10.6.105.160/32                           | instance | true | 00:16:3e:04:f0:95 |
+----------------------+-------------------------------------------+----------+------+-------------------+

...
```

Each listed entry represents an IP allocation of one of the following LXD entities: `network`, `network-forward`, `network-load-balancer`, and `instance`.
An entry contains a list of IP addresses using the CIDR notation.
It also contains a LXD resource URI, the type of the entity, whether it is in NAT mode, and the hardware address (only for the `instance` entity).

