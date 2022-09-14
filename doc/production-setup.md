# Production setup

## Introduction

So you've made it past trying out [LXD live online](https://linuxcontainers.org/lxd/try-it/),
or on a server scavenged from random parts. You like what you see,
and now you want to try doing some serious work with LXD.

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
