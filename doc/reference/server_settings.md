(server-settings)=
# Server settings for a LXD production setup

To allow your LXD server to run a large number of instances, configure the following settings to avoid hitting server limits.

The `Value` column contains the suggested value for each parameter.

## `/etc/security/limits.conf`

```{note}
For users of the snap, those limits are automatically raised.
```

Domain  | Type  | Item      | Value       | Default   | Description
:-----  | :---  | :----     | :---------- | :-------- | :----------
`*`     | soft  | `nofile`  | `1048576`   | unset     | Maximum number of open files
`*`     | hard  | `nofile`  | `1048576`   | unset     | Maximum number of open files
`root`  | soft  | `nofile`  | `1048576`   | unset     | Maximum number of open files
`root`  | hard  | `nofile`  | `1048576`   | unset     | Maximum number of open files
`*`     | soft  | `memlock` | `unlimited` | unset     | Maximum locked-in-memory address space (KB)
`*`     | hard  | `memlock` | `unlimited` | unset     | Maximum locked-in-memory address space (KB)
`root`  | soft  | `memlock` | `unlimited` | unset     | Maximum locked-in-memory address space (KB), only need with `bpf` syscall supervision
`root`  | hard  | `memlock` | `unlimited` | unset     | Maximum locked-in-memory address space (KB), only need with `bpf` syscall supervision

## `/etc/sysctl.conf`

```{note}
Reboot the server after changing any of these parameters.
```

```{config:option} fs.aio-max-nr sysctl
:shortdesc: "Maximum number of concurrent asynchronous I/O operations"
:type: "integer"
:defaultdesc: "`65536`"

Suggested value: `524288`

You might need to increase this limit further if you have a lot of workloads that use the AIO subsystem (for example, MySQL).
```

```{config:option} fs.inotify.max_queued_events sysctl
:shortdesc: "Upper limit on the number of events that can be queued"
:type: "integer"
:defaultdesc: "`16384`"

Suggested value: `1048576`

This option specifies the maximum number of events that can be queued to the corresponding `inotify` instance (see [`inotify`](https://man7.org/linux/man-pages/man7/inotify.7.html) for more information).
```

```{config:option} fs.inotify.max_user_instances sysctl
:shortdesc: "Upper limit on the number of `inotify` instances"
:type: "integer"
:defaultdesc: "`128`"

Suggested value: `1048576`

This option specifies the maximum number of `inotify` instances that can be created per real user ID (see [`inotify`](https://man7.org/linux/man-pages/man7/inotify.7.html) for more information).
```

```{config:option} fs.inotify.max_user_watches sysctl
:shortdesc: "Upper limit on the number of watches"
:type: "integer"
:defaultdesc: "`8192`"

Suggested value: `1048576`

This option specifies the maximum number of watches that can be created per real user ID (see [`inotify`](https://man7.org/linux/man-pages/man7/inotify.7.html) for more information).
```

```{config:option} kernel.dmesg_restrict sysctl
:shortdesc: "Whether to deny access to the messages in the kernel ring buffer"
:type: "integer"
:defaultdesc: "`0`"

Suggested value: `1`

Set this option to `1` to deny container access to the messages in the kernel ring buffer.
Note that setting this value to `1` will also deny access to non-root users on the host system.
```

```{config:option} kernel.keys.maxbytes sysctl
:shortdesc: "Maximum size of the key ring that non-root users can use"
:type: "integer"
:defaultdesc: "`20000`"

Suggested value: `2000000`
```

```{config:option} kernel.keys.maxkeys sysctl
:shortdesc: "Maximum number of keys that a non-root user can use"
:type: "integer"
:defaultdesc: "`200`"

Suggested value: `2000`

Set this option to a value that is higher than the number of instances.
```

```{config:option} net.core.bpf_jit_limit sysctl
:shortdesc: "Limit on the size of eBPF JIT allocations"
:type: "integer"
:defaultdesc: "varies"

Suggested value: `1000000000`

On kernels < 5.15 that are compiled with `CONFIG_BPF_JIT_ALWAYS_ON=y`, this value might limit the amount of instances that can be created.
```

```{config:option} net.ipv4.neigh.default.gc_thresh3 sysctl
:shortdesc: "Maximum number of entries in the IPv4 ARP table"
:type: "integer"
:defaultdesc: "`1024`"

Suggested value: `8192`

Increase this value if you plan to create over 1024 instances.
Otherwise, you will get the error `neighbour: ndisc_cache: neighbor table overflow!` when the ARP table gets full and the instances cannot get a network configuration.
See [`ip-sysctl`](https://www.kernel.org/doc/Documentation/networking/ip-sysctl.txt) for more information.
```

```{config:option} net.ipv6.neigh.default.gc_thresh3 sysctl
:shortdesc: "Maximum number of entries in IPv6 ARP table"
:type: "integer"
:defaultdesc: "`1024`"

Suggested value: `8192`

Increase this value if you plan to create over 1024 instances.
Otherwise, you will get the error `neighbour: ndisc_cache: neighbor table overflow!` when the ARP table gets full and the instances cannot get a network configuration.
See [`ip-sysctl`](https://www.kernel.org/doc/Documentation/networking/ip-sysctl.txt) for more information.
```

```{config:option} vm.max_map_count sysctl
:shortdesc: "Maximum number of memory map areas a process may have"
:type: "integer"
:defaultdesc: "`65530`"

Suggested value: `262144`

Memory map areas are used as a side-effect of calling `malloc`, directly by `mmap` and `mprotect`, and also when loading shared libraries.
```

## Related topics

{{performance_how}}

{{performance_exp}}
