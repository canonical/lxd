# Production setup

## Introduction

So you've made it past trying out [LXD live online](https://linuxcontainers.org/lxd/try-it/),
or on a server scavenged from random parts. You like what you see,
and now you want to try doing some serious work with LXD.

The vast majority of Linux distributions do not come with optimized
kernel settings suitable for the operation of a large number of
containers. The instructions in this document cover the most common
limits that you're likely to hit when running containers and suggested
updated values.

### Common errors that may be encountered

`Failed to allocate directory watch: Too many open files`

`<Error> <Error>: Too many open files`

`failed to open stream: Too many open files in...`

`neighbour: ndisc_cache: neighbor table overflow!`

## Server Changes

### `/etc/security/limits.conf`

Domain  | Type  | Item      | Value     | Default   | Description
:-----  | :---  | :----     | :-------- | :-------- | :----------
`*`     | soft  | `nofile`  | 1048576   | unset     | maximum number of open files
`*`     | hard  | `nofile`  | 1048576   | unset     | maximum number of open files
`root`  | soft  | `nofile`  | 1048576   | unset     | maximum number of open files
`root`  | hard  | `nofile`  | 1048576   | unset     | maximum number of open files
`*`     | soft  | `memlock` | unlimited | unset     | maximum locked-in-memory address space (KB)
`*`     | hard  | `memlock` | unlimited | unset     | maximum locked-in-memory address space (KB)
`root`  | soft  | `memlock` | unlimited | unset     | maximum locked-in-memory address space (KB) (Only need with `bpf` syscall supervision)
`root`  | hard  | `memlock` | unlimited | unset     | maximum locked-in-memory address space (KB) (Only need with `bpf` syscall supervision)

NOTE: For users of the snap, those limits are automatically raised by the snap/LXD.

### `/etc/sysctl.conf`

Parameter                           | Value      | Default   | Description
:-----                              | :---       | :---      | :---
`fs.aio-max-nr`                     | `524288`   | `65536`   | This is the maximum number of concurrent asynchronous I/O operations. You might need to increase it further if you have a lot of workloads that use the AIO subsystem (e.g. MySQL)
`fs.inotify.max_queued_events`      | `1048576`  | `16384`   | This specifies an upper limit on the number of events that can be queued to the corresponding `inotify` instance. [1]
`fs.inotify.max_user_instances`     | `1048576`  | `128`     | This specifies an upper limit on the number of `inotify` instances that can be created per real user ID. [1]
`fs.inotify.max_user_watches`       | `1048576`  | `8192`    | This specifies an upper limit on the number of watches that can be created per real user ID. [1]
`kernel.dmesg_restrict`             | `1`        | `0`       | This denies container access to the messages in the kernel ring buffer. Please note that this also will deny access to non-root users on the host system.
`kernel.keys.maxbytes`              | `2000000`  | `20000`   | This is the maximum size of the key ring non-root users can use
`kernel.keys.maxkeys`               | `2000`     | `200`     | This is the maximum number of keys a non-root user can use, should be higher than the number of containers
`net.ipv4.neigh.default.gc_thresh3` | `8192`     | `1024`    | This is the maximum number of entries in ARP table (IPv4). You should increase this if you create over 1024 containers. Otherwise, you will get the error `neighbour: ndisc_cache: neighbor table overflow!` when the ARP table gets full and those containers will not be able to get a network configuration. [2]
`net.ipv6.neigh.default.gc_thresh3` | `8192`     | `1024`    | This is the maximum number of entries in ARP table (IPv6). You should increase this if you plan to create over 1024 containers. Otherwise, you will get the error `neighbour: ndisc_cache: neighbor table overflow!` when the ARP table gets full and those containers will not be able to get a network configuration. [2]
`vm.max_map_count`                  | `262144`   | `65530`   | This file contains the maximum number of memory map areas a process may have. Memory map areas are used as a side-effect of calling `malloc`, directly by `mmap` and `mprotect`, and also when loading shared libraries.

Then, reboot the server.

[1]: https://man7.org/linux/man-pages/man7/inotify.7.html
[2]: https://www.kernel.org/doc/Documentation/networking/ip-sysctl.txt

### Prevent container name leakage

Both `/sys/kernel/slab` and `/proc/sched_debug` make it easy to list all
cgroups on the system and by extension, all containers.

If this is something you'd like to see blocked, make sure you have the
following done before any container is started:

    chmod 400 /proc/sched_debug
    chmod 700 /sys/kernel/slab/

### Network Bandwidth Tweaking

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
