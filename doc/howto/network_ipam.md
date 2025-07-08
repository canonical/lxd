(network-ipam)=
# How to display IPAM information of a LXD deployment

{abbr}`IPAM (IP Address Management)` is a method used to plan, track, and manage the information associated with a computer network's IP address space. In essence, it's a way of organizing, monitoring, and manipulating the IP space in a network.

Checking the IPAM information for your LXD setup can help you debug networking issues. You can see which IP addresses are used for instances, network interfaces, forwards, and load balancers and use this information to track down where traffic is lost.

`````{tabs}
````{group-tab} CLI

To display IPAM information, enter the following command:

```bash
lxc network list-allocations
```

By default, this command shows the IPAM information for the `default` project. You can select a different project with the `--project` flag, or specify `--all-projects` to display the information for all projects.

The resulting output will look something like this:

```
+----------------------+-----------------+----------+------+-------------------+
|       USED BY        |      ADDRESS    |   TYPE   | NAT  | HARDWARE ADDRESS  |
+----------------------+-----------------+----------+------+-------------------+
| /1.0/networks/lxdbr0 | 192.0.2.0/24    | network  | true |                   |
+----------------------+-----------------+----------+------+-------------------+
| /1.0/networks/lxdbr0 | 2001:db8::/32   | network  | true |                   |
+----------------------+-----------------+----------+------+-------------------+
| /1.0/instances/u1    | 2001:db8::2/128 | instance | true | 00:16:3e:04:f0:95 |
+----------------------+-----------------+----------+------+-------------------+
| /1.0/instances/u1    | 192.0.2.2/32    | instance | true | 00:16:3e:04:f0:95 |
+----------------------+-----------------+----------+------+-------------------+
```

Each listed entry lists the IP address (in CIDR notation) of one of the following LXD entities: `network`, `network-forward`, `network-load-balancer`, and `instance`.
An entry contains an IP address using the CIDR notation.
It also contains a LXD resource URI, the type of the entity, whether it is in NAT mode, and the hardware address (only for the `instance` entity).


````
````{group-tab} UI
View IPAM information from the {guilabel}`Networking` section of the main navigation.
````
`````

## View DHCP leases for fully controlled networks
LXD can provide the currently held DHCP leases for {ref}`fully controlled networks<managed-networks>`:

`````{tabs}
````{group-tab} CLI
To view DHCP lease information, run:

```bash
lxc network list-leases <network_name>
```

For example, using `lxdbr0` from above:

```
+-----------+-------------------+-------------+---------+
| HOSTNAME  |    MAC ADDRESS    | IP ADDRESS  |   TYPE  |
+-----------+-------------------+-------------+---------+
| lxdbr0.gw |                   | 192.0.2.1   | GATEWAY |
+-----------+-------------------+-------------+---------+
| lxdbr0.gw |                   | 2001:db8::1 | GATEWAY |
+-----------+----------+--------+-------------+---------+
| u1        | 00:16:3e:04:f0:95 | 192.0.2.2   | DYNAMIC |
+-----------+-------------------+-------------+---------+
| u1        | 00:16:3e:04:f0:95 | 2001:db8::2 | DYNAMIC |
+-----------+-------------------+-------------+---------+
```

````
````{group-tab} UI

To view DHCP leases, select your fully managed network from the {guilabel}`Networks` page, then open the {guilabel}`Leases` tab.


```{figure} /images/networks/network_view_leases.png
:width: 80%
:alt: View the network IPAM list in LXD
```
````
`````
