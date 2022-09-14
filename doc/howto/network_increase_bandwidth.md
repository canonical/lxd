(network-increase-bandwidth)=
# How to increase the network bandwidth

If you have at least 1GbE NIC on your LXD host with a lot of local
activity (container - container connections, or host - container
connections), or you have 1GbE or better internet connection on your LXD
host it worth play with `txqueuelen`. These settings work even better with
10GbE NIC.

#### Server Changes

##### `txqueuelen`

You need to change `txqueuelen` of your real NIC to 10000 (not sure
about the best possible value for you), and change and change `lxdbr0`
interface `txqueuelen` to 10000.

In Debian-based distributions, you can change `txqueuelen` permanently in `/etc/network/interfaces`.
You can add for example: `up ip link set eth0 txqueuelen 10000` to your interface configuration to set the `txqueuelen` value on boot.
You could set `txqueuelen` temporary (for test purpose) with `ifconfig <interface> txqueuelen 10000`.

##### `/etc/sysctl.conf`

You also need to increase `net.core.netdev_max_backlog` value.
You can add `net.core.netdev_max_backlog = 182757` to `/etc/sysctl.conf` to set it permanently (after reboot)
You set `netdev_max_backlog` temporary (for test purpose) with `echo 182757 > /proc/sys/net/core/netdev_max_backlog`
Note: You can find this value too high, most people prefer set `netdev_max_backlog` = `net.ipv4.tcp_mem` min. value.
For example I use this values `net.ipv4.tcp_mem = 182757 243679 365514`

#### Containers changes

You also need to change the `txqueuelen` value for all your Ethernet interfaces in containers.
In Debian-based distributions, you can change `txqueuelen` permanently in `/etc/network/interfaces`.
You can add for example `up ip link set eth0 txqueuelen 10000` to your interface configuration to set the `txqueuelen` value on boot.

#### Notes regarding this change

10000 `txqueuelen` value commonly used with 10GbE NICs. Basically small
`txqueuelen` values used with slow devices with a high latency, and higher
with devices with low latency. I personally have like 3-5% improvement
with these settings for local (host with container, container vs
container) and internet connections. Good thing about `txqueuelen` value
tweak, the more containers you use, the more you can be can benefit from
this tweak. And you can always temporary set this values and check this
tweak in your environment without LXD host reboot.
