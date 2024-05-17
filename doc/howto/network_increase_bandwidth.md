(network-increase-bandwidth)=
# How to increase the network bandwidth

You can increase the network bandwidth of your LXD setup by configuring the transmit queue length (`txqueuelen`).
This change makes sense in the following scenarios:

- You have a NIC with 1 GbE or higher on a LXD host with a lot of local activity (instance-instance connections or host-instance connections).
- You have an internet connection with 1 GbE or higher on your LXD host.

The more instances you use, the more you can benefit from this tweak.

```{note}
The following instructions use a `txqueuelen` value of 10000, which is commonly used with 10GbE NICs, and a `net.core.netdev_max_backlog` value of 182757.
Depending on your network, you might need to use different values.

In general, you should use small `txqueuelen` values with slow devices with a high latency, and high `txqueuelen` values with devices with a low latency.
For the `net.core.netdev_max_backlog` value, a good guideline is to use the minimum value of the `net.ipv4.tcp_mem` configuration.
```

## Increase the network bandwidth on the LXD host

````{tabs}

```{group-tab} Ubuntu >= 18.04

Complete the following steps to increase the network bandwidth on the LXD host:

1. Increase the transmit queue length (`txqueuelen`) of both the real NIC (for example, `enp5s0f1`) and the LXD NIC (for example, `lxdbr0`).
   To make the change permanent, create a file named `/etc/udev/rules.d/60-custom-txqueuelen.rules` with the following content:

       KERNEL=="enp5s0f1", RUN+="/sbin/ip link set %k txqueuelen 10000"
       KERNEL=="lxdbr0", RUN+="/sbin/ip link set %k txqueuelen 10000"
   Apply the above `udev` rules via:

        udevadm trigger

1. Increase the receive queue length (`net.core.netdev_max_backlog`).
   To make the change permanent, add the following configuration to `/etc/sysctl.conf`:

       net.core.netdev_max_backlog = 182757

   Apply the above `sysctl.conf` change via:

        sysctl -p
```
```{group-tab} Ubuntu <= 17.04

Complete the following steps to increase the network bandwidth on the LXD host:

1. Increase the transmit queue length (`txqueuelen`) of both the real NIC and the LXD NIC (for example, `lxdbr0`).
   You can do this temporarily for testing with the following command:

       ifconfig <interface> txqueuelen 10000

   To make the change permanent, add the following command to your interface configuration in `/etc/network/interfaces`:

       up ip link set eth0 txqueuelen 10000

1. Increase the receive queue length (`net.core.netdev_max_backlog`).
   You can do this temporarily for testing with the following command:

       echo 182757 > /proc/sys/net/core/netdev_max_backlog

   To make the change permanent, add the following configuration to `/etc/sysctl.conf`:

       net.core.netdev_max_backlog = 182757
```

````

## Increase the transmit queue length on the instances

You must also change the `txqueuelen` value for all Ethernet interfaces in your instances.
To do this, use one of the following methods:

- Apply the same changes as described above for the LXD host.
- Set the `queue.tx.length` device option on the instance profile or configuration.
  For example, to do this for the LXD default profile:

       lxc profile device set default eth0 queue.tx.length "10000"
