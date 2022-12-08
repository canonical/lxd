(instance-options)=
# Instance options

Instance options are configuration options that are directly related to the instance.

See {ref}`instances-configure-options` for instructions on how to set the instance options.

The key/value configuration is namespaced.
The following options are available:

- {ref}`instance-options-misc`
- {ref}`instance-options-boot`
- [`cloud-init` configuration](instance-options-cloud-init)
- {ref}`instance-options-limits`
- {ref}`instance-options-migration`
- {ref}`instance-options-nvidia`
- {ref}`instance-options-raw`
- {ref}`instance-options-security`
- {ref}`instance-options-snapshots`
- {ref}`instance-options-volatile`

Note that while a type is defined for each option, all values are stored as strings and should be exported over the REST API as strings (which makes it possible to support any extra values without breaking backward compatibility).

(instance-options-misc)=
## Miscellaneous options

In addition to the configuration options listed in the following sections, these instance options are supported:

```{rst-class} dec-font-size break-col-1 min-width-1-15
```

Key                                             | Type      | Default           | Live update   | Condition                 | Description
:--                                             | :---      | :------           | :----------   | :----------               | :----------
`agent.nic_config`                              | bool      | `false`           | no            | virtual machine           | Controls whether to set the name and MTU of the default network interfaces to be the same as the instance devices (this happens automatically for containers)
`cluster.evacuate`                              | string    | `auto`            | no            | -                         | Controls what to do when evacuating the instance (`auto`, `migrate`, `live-migrate`, or `stop`)
`environment.*`                                 | string    | -                 | yes (exec)    | -                         | Key/value environment variables to export to the instance and set for `lxc exec`
`linux.kernel_modules`                          | string    | -                 | yes           | container                 | Comma-separated list of kernel modules to load before starting the instance
`linux.sysctl.*`                                | string    | -                 | no            | container                 | Controls whether to allow modifying `sysctl` settings
`user.*`                                        | string    | -                 | no            | -                         | Free-form user key/value storage (can be used in search)

(instance-options-boot)=
## Boot-related options

The following instance options control the boot-related behavior of the instance:

```{rst-class} dec-font-size break-col-1 min-width-1-15
```

Key                                             | Type      | Default           | Live update   | Condition                 | Description
:--                                             | :---      | :------           | :----------   | :----------               | :----------
`boot.autostart`                                | bool      | -                 | no            | -                         | Controls whether to always start the instance when LXD starts (if not set, restore the last state)
`boot.autostart.delay`                          | integer   | `0`               | no            | -                         | Number of seconds to wait after the instance started before starting the next one
`boot.autostart.priority`                       | integer   | `0`               | no            | -                         | What order to start the instances in (starting with the highest value)
`boot.host_shutdown_timeout`                    | integer   | `30`              | yes           | -                         | Seconds to wait for the instance to shut down before it is force-stopped
`boot.stop.priority`                            | integer   | `0`               | no            | -                         | What order to shut down the instances in (starting with the highest value)

(instance-options-cloud-init)=
## `cloud-init` configuration

The following instance options control the [`cloud-init`](cloud-init) configuration of the instance:

```{rst-class} dec-font-size break-col-1 min-width-1-15
```

Key                                             | Type      | Default           | Live update   | Condition                 | Description
:--                                             | :---      | :------           | :----------   | :----------               | :----------
`cloud-init.network-config`                     | string    | `DHCP on eth0`    | no            | if supported by image     | Network configuration for `cloud-init` (content is used as seed value)
`cloud-init.user-data`                          | string    | `#cloud-config`   | no            | if supported by image     | User data for `cloud-init` (content is used as seed value)
`cloud-init.vendor-data`                        | string    | `#cloud-config`   | no            | if supported by image     | Vendor data for `cloud-init` (content is used as seed value)
`user.meta-data`                                | string    | -                 | no            | if supported by image     | Legacy meta-data for `cloud-init` (content is appended to seed value)
`user.network-config`                           | string    | `DHCP on eth0`    | no            | if supported by image     | Legacy version of `cloud-init.network-config`
`user.user-data`                                | string    | `#cloud-config`   | no            | if supported by image     | Legacy version of `cloud-init.user-data`
`user.vendor-data`                              | string    | `#cloud-config`   | no            | if supported by image     | Legacy version of `cloud-init.vendor-data`

Support for these options depends on the image that is used and is not guaranteed.

If you specify both `cloud-init.user-data` and `cloud-init.vendor-data`, the content of both options is merged.
Therefore, make sure that the `cloud-init` configuration you specify in those options does not contain the same keys.

(instance-options-limits)=
## Resource limits

The following instance options specify resource limits for the instance:

```{rst-class} dec-font-size break-col-1 min-width-1-15
```

Key                                             | Type      | Default           | Live update   | Condition                 | Description
:--                                             | :---      | :------           | :----------   | :----------               | :----------
`limits.cpu`                                    | string    | for VMs: 1 CPU    | yes           | -                         | Number or range of CPUs to expose to the instance; see {ref}`instance-options-limits-cpu`
`limits.cpu.allowance`                          | string    | `100%`            | yes           | container                 | Controls how much of the CPU can be used: either a percentage (`50%`) for a soft limit or a chunk of time (`25ms/100ms`) for a hard limit; see {ref}`instance-options-limits-cpu-container`
`limits.cpu.priority`                           | integer   | `10` (maximum)    | yes           | container                 | CPU scheduling priority compared to other instances sharing the same CPUs when overcommitting resources (integer between 0 and 10); see {ref}`instance-options-limits-cpu-container`
`limits.disk.priority`                          | integer   | `5` (medium)      | yes           | -                         | Controls how much priority to give to the instance's I/O requests when under load (integer between 0 and 10)
`limits.hugepages.64KB`                         | string    | -                 | yes           | container                 | Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 64 KB huge pages; see {ref}`instance-options-limits-hugepages`
`limits.hugepages.1MB`                          | string    | -                 | yes           | container                 | Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 1 MB huge pages; see {ref}`instance-options-limits-hugepages`
`limits.hugepages.2MB`                          | string    | -                 | yes           | container                 | Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 2 MB huge pages; see {ref}`instance-options-limits-hugepages`
`limits.hugepages.1GB`                          | string    | -                 | yes           | container                 | Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 1 GB huge pages; see {ref}`instance-options-limits-hugepages`
`limits.kernel.*`                               | string    | -                 | no            | container                 | Kernel resources per instance (for example, number of open files); see {ref}`instance-options-limits-kernel`
`limits.memory`                                 | string    | for VMs: `1Gib`   | yes           | -                         | Percentage of the host's memory or fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`)
`limits.memory.enforce`                         | string    | `hard`            | yes           | container                 | If `hard`, the instance cannot exceed its memory limit; if `soft`, the instance can exceed its memory limit when extra host memory is available
`limits.memory.hugepages`                       | bool      | `false`           | no            | virtual machine           | Controls whether to back the instance using huge pages rather than regular system memory
`limits.memory.swap`                            | bool      | `true`            | yes           | container                 | Controls whether to encourage/discourage swapping less used pages for this instance
`limits.memory.swap.priority`                   | integer   | `10` (maximum)    | yes           | container                 | Prevents the instance from being swapped to disk (integer between 0 and 10; the higher the value, the less likely the instance is to be swapped to disk)
`limits.network.priority`                       | integer   | `0` (minimum)     | yes           | -                         | Controls how much priority to give to the instance's network requests when under load (integer between 0 and 10)
`limits.processes`                              | integer   | - (max)           | yes           | container                 | Maximum number of processes that can run in the instance

### CPU limits

The CPU limits are implemented through a mix of the `cpuset` and `cpu` cgroup controllers.

(instance-options-limits-cpu)=
#### CPU pinning

`limits.cpu` results in CPU pinning through the `cpuset` controller.
You can specify either which CPUs to use or how many CPUs to use:

- To specify which CPUs to use, set `limits.cpu` to either a set of CPUs (for example, `1,2,3`) or a CPU range (for example, `0-3`).

  To pin to a single CPU, use the range syntax (for example, `1-1`) to differentiate it from a number of CPUs.
- If you specify a number (for example, `4`) of CPUs, LXD will do dynamic load-balancing of all instances that aren't pinned to specific CPUs, trying to spread the load on the machine.
  Instances are re-balanced every time an instance starts or stops, as well as whenever a CPU is added to the system.

```{note}
LXD virtual machines default to having just one vCPU allocated, which shows up as matching the host CPU vendor and type, but has a single core and no threads.

When `limits.cpu` is set to a single integer, LXD allocates multiple vCPUs and exposes them to the guest as full cores.
Those vCPUs are not pinned to specific physical cores on the host.
The number of vCPUs can be updated while the VM is running.

When `limits.cpu` is set to a range or comma-separated list of CPU IDs (as provided by `lxc info --resources`), the vCPUs are pinned to those physical cores.
In this scenario, LXD checks whether the CPU configuration lines up with a realistic hardware topology and if it does, it replicates that topology in the guest.
When doing CPU pinning, it is not possible to change the configuration while the VM is running.

For example, if the pinning configuration includes eight threads, with each pair of thread coming from the same core and an even number of cores spread across two CPUs, the guest will show two CPUs, each with two cores and each core with two threads.
The NUMA layout is similarly replicated and in this scenario, the guest would most likely end up with two NUMA nodes, one for each CPU socket.

In such an environment with multiple NUMA nodes, the memory is similarly divided across NUMA nodes and be pinned accordingly on the host and then exposed to the guest.

All this allows for very high performance operations in the guest as the guest scheduler can properly reason about sockets, cores and threads as well as consider NUMA topology when sharing memory or moving processes across NUMA nodes.
```

(instance-options-limits-cpu-container)=
#### Allowance and priority (container only)

`limits.cpu.allowance` drives either the CFS scheduler quotas when passed a time constraint, or the generic CPU shares mechanism when passed a percentage value:

- The time constraint (for example, `20ms/50ms`) is relative to one CPU worth of time, so to restrict to two CPUs worth of time, use something like `100ms/50ms`.
- When using a percentage value, the limit is applied only when under load.
  It is used to calculate the scheduler priority for the instance, relative to any other instance that is using the same CPU or CPUs.

`limits.cpu.priority` is another factor that is used to compute the scheduler priority score when a number of instances sharing a set of CPUs have the same percentage of CPU assigned to them.

(instance-options-limits-hugepages)=
### Huge page limits

LXD allows to limit the number of huge pages available to a container through the `limits.hugepage.[size]` key.

Architectures often expose multiple huge-page sizes.
The available huge-page sizes depend on the architecture.

Setting limits for huge pages is especially useful when LXD is configured to intercept the `mount` syscall for the `hugetlbfs` file system in unprivileged containers.
When LXD intercepts a `hugetlbfs` `mount` syscall, it mounts the `hugetlbfs` file system for a container with correct `uid` and `gid` values as mount options.
This makes it possible to use huge pages from unprivileged containers.
However, it is recommended to limit the number of huge pages available to the container through `limits.hugepages.[size]` to stop the container from being able to exhaust the huge pages available to the host.

Limiting huge pages is done through the `hugetlb` cgroup controller, which means that the host system must expose the `hugetlb` controller in the legacy or unified cgroup hierarchy for these limits to apply.

(instance-options-limits-kernel)=
### Kernel resource limits

LXD exposes a generic namespaced key `limits.kernel.*` that can be used to set resource limits for an instance.

It is generic in the sense that LXD does not perform any validation on the resource that is specified following the `limits.kernel.*` prefix.
LXD cannot know about all the possible resources that a given kernel supports.
Instead, LXD simply passes down the corresponding resource key after the `limits.kernel.*` prefix and its value to the kernel.
The kernel does the appropriate validation.
This allows users to specify any supported limit on their system.

Some common limits are:

Key                       | Resource          | Description
:--                       | :---              | :----------
`limits.kernel.as`        | `RLIMIT_AS`       | Maximum size of the process's virtual memory
`limits.kernel.core`      | `RLIMIT_CORE`     | Maximum size of the process's core dump file
`limits.kernel.cpu`       | `RLIMIT_CPU`      | Limit in seconds on the amount of CPU time the process can consume
`limits.kernel.data`      | `RLIMIT_DATA`     | Maximum size of the process's data segment
`limits.kernel.fsize`     | `RLIMIT_FSIZE`    | Maximum size of files the process may create
`limits.kernel.locks`     | `RLIMIT_LOCKS`    | Limit on the number of file locks that this process may establish
`limits.kernel.memlock`   | `RLIMIT_MEMLOCK`  | Limit on the number of bytes of memory that the process may lock in RAM
`limits.kernel.nice`      | `RLIMIT_NICE`     | Maximum value to which the process's nice value can be raised
`limits.kernel.nofile`    | `RLIMIT_NOFILE`   | Maximum number of open files for the process
`limits.kernel.nproc`     | `RLIMIT_NPROC`    | Maximum number of processes that can be created for the user of the calling process
`limits.kernel.rtprio`    | `RLIMIT_RTPRIO`   | Maximum value on the real-time-priority that may be set for this process
`limits.kernel.sigpending`| `RLIMIT_SIGPENDING` | Maximum number of signals that may be queued for the user of the calling process

A full list of all available limits can be found in the manpages for the `getrlimit(2)`/`setrlimit(2)` system calls.

To specify a limit within the `limits.kernel.*` namespace, use the resource name in lowercase without the `RLIMIT_` prefix.
For example, `RLIMIT_NOFILE` should be specified as `nofile`.

A limit is specified as two colon-separated values that are either numeric or the word `unlimited` (for example, `limits.kernel.nofile=1000:2000`).
A single value can be used as a shortcut to set both soft and hard limit to the same value (for example, `limits.kernel.nofile=3000`).

A resource with no explicitly configured limit will inherit its limit from the process that starts up the instance.
Note that this inheritance is not enforced by LXD but by the kernel.

(instance-options-migration)=
## Migration options

The following instance options control the behavior if the instance is {ref}`moved from one LXD server to another <move-instances>`:

```{rst-class} dec-font-size break-col-1 min-width-1-15
```

Key                                             | Type      | Default           | Live update   | Condition                 | Description
:--                                             | :---      | :------           | :----------   | :----------               | :----------
`migration.incremental.memory`                  | bool      | `false`           | yes           | container                 | Controls whether to use incremental memory transfer of the instance's memory to reduce downtime
`migration.incremental.memory.goal`             | integer   | `70`              | yes           | container                 | Percentage of memory to have in sync before stopping the instance
`migration.incremental.memory.iterations`       | integer   | `10`              | yes           | container                 | Maximum number of transfer operations to go through before stopping the instance
`migration.stateful`                            | bool      | `false`           | no            | virtual machine           | Controls whether to allow for stateful stop/start and snapshots (enabling this prevents the use of some features that are incompatible with it)

(instance-options-nvidia)=
## NVIDIA and CUDA configuration

The following instance options specify the NVIDIA and CUDA configuration of the instance:

```{rst-class} dec-font-size break-col-1 min-width-1-15
```

Key                                             | Type      | Default           | Live update   | Condition                 | Description
:--                                             | :---      | :------           | :----------   | :----------               | :----------
`nvidia.driver.capabilities`                    | string    | `compute,utility` | no            | container                 | What driver capabilities the instance needs (sets `libnvidia-container` `NVIDIA_DRIVER_CAPABILITIES`)
`nvidia.runtime`                                | bool      | `false`           | no            | container                 | Controls whether to pass the host NVIDIA and CUDA runtime libraries into the instance
`nvidia.require.cuda`                           | string    | -                 | no            | container                 | Version expression for the required CUDA version (sets `libnvidia-container` `NVIDIA_REQUIRE_CUDA`)
`nvidia.require.driver`                         | string    | -                 | no            | container                 | Version expression for the required driver version (sets `libnvidia-container` `NVIDIA_REQUIRE_DRIVER`)

(instance-options-raw)=
## Raw instance configuration overrides

The following instance options allow direct interaction with the backend features that LXD itself uses:

```{rst-class} dec-font-size break-col-1 min-width-1-15
```

Key                                             | Type      | Default           | Live update   | Condition                 | Description
:--                                             | :---      | :------           | :----------   | :----------               | :----------
`raw.apparmor`                                  | blob      | -                 | yes           | -                         | AppArmor profile entries to be appended to the generated profile
`raw.idmap`                                     | blob      | -                 | no            | unprivileged container    | Raw idmap configuration (for example, `both 1000 1000`)
`raw.lxc`                                       | blob      | -                 | no            | container                 | Raw LXC configuration to be appended to the generated one
`raw.qemu`                                      | blob      | -                 | no            | virtual machine           | Raw QEMU configuration to be appended to the generated command line
`raw.qemu.conf`                                 | blob      | -                 | no            | virtual machine           | Addition/override to the generated `qemu.conf` file (see {ref}`instance-options-qemu`)
`raw.seccomp`                                   | blob      | -                 | no            | container                 | Raw Seccomp configuration

```{important}
Setting these `raw.*` keys might break LXD in non-obvious ways.
Therefore, you should avoid setting any of these keys.
```

(instance-options-qemu)=
### Override QEMU configuration

For VM instances, LXD configures QEMU through a configuration file that is passed to QEMU with the `-readconfig` command-line option.
This configuration file is generated for each instance before boot.
It can be found at `/var/log/lxd/<instance_name>/qemu.conf`.

The default configuration works fine for LXD's most common use case: modern UEFI guests with VirtIO devices.
In some situations, however, you might need to override the generated configuration.
For example:

- To run an old guest OS that doesn't support UEFI.
- To specify custom virtual devices when VirtIO is not supported by the guest OS.
- To add devices that are not supported by LXD before the machines boots.
- To remove devices that conflict with the guest OS.

To override the configuration, set the `raw.qemu.conf` option.
It supports a format similar to `qemu.conf`, with some additions.
Since it is a multi-line configuration option, you can use it to modify multiple sections or keys.

- To replace a section or key in the generated configuration file, add a section with a different value.

  For example, use the following section to override the default `virtio-gpu-pci` GPU driver:

  ```
  raw.qemu.conf: |-
      [device "qemu_gpu"]
      driver = "qxl-vga"
  ```

- To remove a section, specify a section without any keys.
  For example:

  ```
  raw.qemu.conf: |-
      [device "qemu_gpu"]
  ```

- To remove a key, specify an empty string as the value.
  For example:

  ```
  raw.qemu.conf: |-
      [device "qemu_gpu"]
      driver = ""
  ```

- To add a new section, specify a section name that is not present in the configuration file.

The configuration file format used by QEMU allows multiple sections with the same name.
Here's a piece of the configuration generated by LXD:

```
[global]
driver = "ICH9-LPC"
property = "disable_s3"
value = "1"

[global]
driver = "ICH9-LPC"
property = "disable_s4"
value = "1"
```

To specify which section to override, specify an index.
For example:

```
raw.qemu.conf: |-
    [global][1]
    value = "0"
```

Section indexes start at 0 (which is the default value when not specified), so the above example would generate the following configuration:

```
[global]
driver = "ICH9-LPC"
property = "disable_s3"
value = "1"

[global]
driver = "ICH9-LPC"
property = "disable_s4"
value = "0"
```

(instance-options-security)=
## Security policies

The following instance options control the {ref}`security` policies of the instance:

```{rst-class} dec-font-size break-col-1 min-width-1-15
```

Key                                             | Type      | Default           | Live update   | Condition                 | Description
:--                                             | :---      | :------           | :----------   | :----------               | :----------
`security.devlxd`                               | bool      | `true`            | no            | -                         | Controls the presence of `/dev/lxd` in the instance
`security.devlxd.images`                        | bool      | `false`           | no            | container                 | Controls the availability of the `/1.0/images` API over `devlxd`
`security.idmap.base`                           | integer   | -                 | no            | unprivileged container    | The base host ID to use for the allocation (overrides auto-detection)
`security.idmap.isolated`                       | bool      | `false`           | no            | unprivileged container    | Controls whether to use an idmap for this instance that is unique among instances with isolated set
`security.idmap.size`                           | integer   | -                 | no            | unprivileged container    | The size of the idmap to use
`security.nesting`                              | bool      | `false`           | yes           | container                 | Controls whether to support running LXD (nested) inside the instance
`security.privileged`                           | bool      | `false`           | no            | container                 | Controls whether to run the instance in privileged mode
`security.protection.delete`                    | bool      | `false`           | yes           | -                         | Prevents the instance from being deleted
`security.protection.shift`                     | bool      | `false`           | yes           | container                 | Prevents the instance's file system from being UID/GID shifted on startup
`security.agent.metrics`                        | bool      | `true`            | no            | virtual machine           | Controls whether the `lxd-agent` is queried for state information and metrics
`security.secureboot`                           | bool      | `true`            | no            | virtual machine           | Controls whether UEFI secure boot is enabled with the default Microsoft keys
`security.syscalls.allow`                       | string    | -                 | no            | container                 | A `\n`-separated list of syscalls to allow (mutually exclusive with `security.syscalls.deny*`)
`security.syscalls.deny`                        | string    | -                 | no            | container                 | A `\n`-separated list of syscalls to deny
`security.syscalls.deny_compat`                 | bool      | `false`           | no            | container                 | On `x86_64`, controls whether to block `compat_*` syscalls (no-op on other architectures)
`security.syscalls.deny_default`                | bool      | `true`            | no            | container                 | Controls whether to enable the default syscall deny
`security.syscalls.intercept.bpf`               | bool      | `false`           | no            | container                 | Controls whether to handle the `bpf` system call
`security.syscalls.intercept.bpf.devices`       | bool      | `false`           | no            | container                 | Controls whether to allow `bpf` programs for the devices cgroup in the unified hierarchy to be loaded
`security.syscalls.intercept.mknod`             | bool      | `false`           | no            | container                 | Controls whether to handle the `mknod` and `mknodat` system calls (allows creation of a limited subset of char/block devices)
`security.syscalls.intercept.mount`             | bool      | `false`           | no            | container                 | Controls whether to handle the `mount` system call
`security.syscalls.intercept.mount.allowed`     | string    | -                 | yes           | container                 | A comma-separated list of file systems that are safe to mount for processes inside the instance
`security.syscalls.intercept.mount.fuse`        | string    | -                 | yes           | container                 | Mounts of a given file system that should be redirected to their FUSE implementation (for example, `ext4=fuse2fs`)
`security.syscalls.intercept.mount.shift`       | bool      | `false`           | yes           | container                 | Controls whether to mount `shiftfs` on top of file systems handled through mount syscall interception
`security.syscalls.intercept.sched_setscheduler`| bool      | `false`           | no            | container                 | Controls whether to handle the `sched_setscheduler` system call (allows increasing process priority)
`security.syscalls.intercept.setxattr`          | bool      | `false`           | no            | container                 | Controls whether to handle the `setxattr` system call (allows setting a limited subset of restricted extended attributes)
`security.syscalls.intercept.sysinfo`           | bool      | `false`           | no            | container                 | Controls whether to handle the `sysinfo` system call (to get cgroup-based resource usage information)

(instance-options-snapshots)=
## Snapshot scheduling and configuration

The following instance options control the creation and expiry of {ref}`instance snapshots <instances-snapshots>`:

```{rst-class} dec-font-size break-col-1 min-width-1-15
```

Key                                             | Type      | Default           | Live update   | Condition                 | Description
:--                                             | :---      | :------           | :----------   | :----------               | :----------
`snapshots.schedule`                            | string    | -                 | no            | -                         | {{snapshot_schedule_format}}
`snapshots.schedule.stopped`                    | bool      | `false`           | no            | -                         | Controls whether to automatically snapshot stopped instances
`snapshots.pattern`                             | string    | `snap%d`          | no            | -                         | {{snapshot_pattern_format}}; see {ref}`instance-options-snapshots-names`
`snapshots.expiry`                              | string    | -                 | no            | -                         | {{snapshot_expiry_format}}

(instance-options-snapshots-names)=
### Automatic snapshot names

{{snapshot_pattern_detail}}

(instance-options-volatile)=
## Volatile internal data

The following volatile keys are currently used internally by LXD to store internal data specific to an instance:

```{rst-class} dec-font-size break-col-1 min-width-1-15
```

Key                                         | Type      | Description
:--                                         | :---      | :----------
`volatile.apply_template`                   | string    | The name of a template hook that should be triggered upon next startup
`volatile.apply_nvram`                      | string    | Whether to regenerate VM NVRAM the next time the instance starts
`volatile.base_image`                       | string    | The hash of the image the instance was created from (if any)
`volatile.cloud-init.instance-id`           | string    | The `instance-id` (UUID) exposed to `cloud-init`
`volatile.evacuate.origin`                  | string    | The origin (cluster member) of the evacuated instance
`volatile.idmap.base`                       | integer   | The first ID in the instance's primary idmap range
`volatile.idmap.current`                    | string    | The idmap currently in use by the instance
`volatile.idmap.next`                       | string    | The idmap to use the next time the instance starts
`volatile.last_state.idmap`                 | string    | Serialized instance UID/GID map
`volatile.last_state.power`                 | string    | Instance state as of last host shutdown
`volatile.vsock_id`                         | string    | Instance `vsock` ID used as of last start
`volatile.uuid`                             | string    | Instance UUID (globally unique across all servers and projects)
`volatile.<name>.apply_quota`               | string    | Disk quota to be applied the next time the instance starts
`volatile.<name>.ceph_rbd`                  | string    | RBD device path for Ceph disk devices
`volatile.<name>.host_name`                 | string    | Network device name on the host
`volatile.<name>.hwaddr`                    | string    | Network device MAC address (when no `hwaddr` property is set on the device itself)
`volatile.<name>.last_state.created`        | string    | Whether the network device physical device was created (`true` or `false`)
`volatile.<name>.last_state.mtu`            | string    | Network device original MTU used when moving a physical device into an instance
`volatile.<name>.last_state.hwaddr`         | string    | Network device original MAC used when moving a physical device into an instance
`volatile.<name>.last_state.vf.id`          | string    | SR-IOV virtual function ID used when moving a VF into an instance
`volatile.<name>.last_state.vf.hwaddr`      | string    | SR-IOV virtual function original MAC used when moving a VF into an instance
`volatile.<name>.last_state.vf.vlan`        | string    | SR-IOV virtual function original VLAN used when moving a VF into an instance
`volatile.<name>.last_state.vf.spoofcheck`  | string    | SR-IOV virtual function original spoof check setting used when moving a VF into an instance

```{note}
Volatile keys cannot be set by the user.
```
