# Production setup
## Introduction
So you've made it past trying out [LXD live online](https://linuxcontainers.org/lxd/try-it/),
or on a server scavenged from random parts. You like what you see,
and now you want to try doing some serious work with LXD.

With the vanilla installation of Ubuntu Server 16.04, some modifications
to the server configuration will be needed, to avoid common pitfalls when
using containers that require tens of thousands of file operations.


### Common errors that may be encountered

`Failed to allocate directory watch: Too many open files`

`<Error> <Error>: Too many open files`

`failed to open stream: Too many open files in...`


## Server Changes
### /etc/security/limits.conf

Domain  | Type  | Item    | Value     | Default   | Description
:-----  | :---  | :----   | :-------- | :-------- | :----------
\*      | soft  | nofile  | 1048576   | unset     | maximum number of open files
\*      | hard  | nofile  | 1048576   | unset     | maximum number of open files
root    | soft  | nofile  | 1048576   | unset     | maximum number of open files
root    | hard  | nofile  | 1048576   | unset     | maximum number of open files
\*      | soft  | memlock | unlimited | unset     | maximum locked-in-memory address space (KB)
\*      | hard  | memlock | unlimited | unset     | maximum locked-in-memory address space (KB)


### /etc/sysctl.conf

Parameter                       | Value     | Default | Description
:-----                          | :---      | :---    | :---
fs.inotify.max\_queued\_events  | 1048576   | 16384   | This specifies an upper limit on the number of events that can be queued to the corresponding inotify instance. [1]
fs.inotify.max\_user\_instances | 1048576   | 128     | This specifies an upper limit on the number of inotify instances that can be created per real user ID. [1]
fs.inotify.max\_user\_watches   | 1048576   | 8192    | This specifies an upper limit on the number of watches that can be created per real user ID. [1]
vm.max\_map\_count              | 262144    | 65530   | This file contains the maximum number of memory map areas a process may have. Memory map areas are used as a side-effect of calling malloc, directly by mmap and mprotect, and also when loading shared libraries.
kernel.dmesg\_restrict          | 1         | 0       | This denies container access to the messages in the kernel ring buffer. Please note that this also will deny access to non-root users on the host system.


Then, reboot the server.


[1]: http://man7.org/linux/man-pages/man7/inotify.7.html

### Network Bandwidth Tweaking 
If you have at least 1GbE NIC on your lxd host with a lot of local activity (container - container connections, or host - container connections), or you have 1GbE or better internet connection on your lxd host it worth play with txqueuelen. These settings work even better with 10GbE NIC.

#### Server Changes

##### txqueuelen 

You need to change `txqueuelen` of your real NIC to 10000 (not sure about the best possible value for you), and change and change lxdbr0 interface `txqueuelen` to 10000.  
In Debian-based distros you can change `txqueuelen` permanently in `/etc/network/interfaces`  
You can add for ex.: `up ip link set eth0 txqueuelen 10000` to your interface configuration to set txqueuelen value on boot.  
For permanent lxdbr0 txqueuelen value change I prefer edit `/usr/lib/lxd/lxd-bridge`. You can add `ifconfig lxdbr0 txqueuelen 10000` in start section, just after iptables rules. For ex.:

```bash
iptables "${use_iptables_lock}" -I FORWARD -o "${LXD_BRIDGE}" -j ACCEPT -m comment --comment "managed by lxd-bridge"
iptables "${use_iptables_lock}" -t mangle -A POSTROUTING -o "${LXD_BRIDGE}" -p udp -m udp --dport 68 -j CHECKSUM --checksum-fill -m comment --comment "managed by lxd-bridge"
ifconfig lxdbr0 txqueuelen 10000
```

If you use lxd master in production or find this inappropriate you can set in `rc.local` or in another way you like.
You could set it txqueuelen temporary (for test purpose) with `ifconfig <interface> txqueuelen 10000`

##### /etc/sysctl.conf

You also need to increase `net.core.netdev_max_backlog` value.  
You can add `net.core.netdev_max_backlog = 182757` to `/etc/sysctl.conf` to set it permanently (after reboot)
You set `netdev_max_backlog` temporary (for test purpose) with `echo 182757 > /proc/sys/net/core/netdev_max_backlog`
Note: You can find this value too high, most people prefer set `netdev_max_backlog` = `net.ipv4.tcp_mem` min. value.
For example I use this values `net.ipv4.tcp_mem = 182757 243679 365514`

#### Containers changes

You also need to change txqueuelen value for all you ethernet interfaces in containers.  
In Debian-based distros you can change txqueuelen permanently in `/etc/network/interfaces`  
You can add for ex.: `up ip link set eth0 txqueuelen 10000` to your interface configuration to set txqueuelen value on boot.

#### Notes regarding this change

10000 txqueuelen value commonly used with 10GbE NICs. Basically small txqueuelen values used with slow devices with a high latency, and higher with devices with low latency. I personally have like 3-5% improvement with these settings for local (host with container, container vs container) and internet connections. Good thing about txqueuelen value tweak, the more containers you use, the more you can be can benefit from this tweak. And you can always temporary set this values and check this tweak in your environment without lxd host reboot.



