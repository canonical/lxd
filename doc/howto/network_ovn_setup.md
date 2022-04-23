(network-ovn-setup)=
# How to set up OVN with LXD

```{youtube} https://www.youtube.com/watch?v=1M__Rm9iZb8
```

This will create a standalone OVN network that is connected to the parent network lxdbr0 for outbound connectivity.

Install the OVN tools and configure the OVN integration bridge on the local server:

```
sudo apt install ovn-host ovn-central
sudo ovs-vsctl set open_vswitch . \
  external_ids:ovn-remote=unix:/var/run/ovn/ovnsb_db.sock \
  external_ids:ovn-encap-type=geneve \
  external_ids:ovn-encap-ip=127.0.0.1
```

Create an OVN network and an instance using it:

```
lxc network set lxdbr0 ipv4.dhcp.ranges=... ipv4.ovn.ranges=... # Allocate IP range for OVN gateways.
lxc network create ovntest --type=ovn network=lxdbr0
lxc init ubuntu:22.04 c1
lxc config device override c1 eth0 network=ovntest
lxc start c1
lxc ls
+------+---------+---------------------+----------------------------------------------+-----------+-----------+
| NAME |  STATE  |        IPV4         |                     IPV6                     |   TYPE    | SNAPSHOTS |
+------+---------+---------------------+----------------------------------------------+-----------+-----------+
| c1   | RUNNING | 10.254.118.2 (eth0) | fd42:887:cff3:5089:216:3eff:fef0:549f (eth0) | CONTAINER | 0         |
+------+---------+---------------------+----------------------------------------------+-----------+-----------+
```
