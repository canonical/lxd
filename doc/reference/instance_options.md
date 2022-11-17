(instance-options)=
# Instance options

The key/value configuration is namespaced with the following namespaces
currently supported:

- `boot` (boot related options, timing, dependencies, ...)
- `cloud-init` (cloud-init configuration)
- `environment` (environment variables)
- `image` (copy of the image properties at time of creation)
- `limits` (resource limits)
- `nvidia` (NVIDIA and CUDA configuration)
- `raw` (raw instance configuration overrides)
- `security` (security policies)
- `user` (storage for user properties, searchable)
- `volatile` (used internally by LXD to store internal data specific to an instance)

The currently supported keys are:

```{rst-class} dec-font-size break-col-1 min-width-1-15
```

(instance-configuration)=
Key                                             | Type      | Default           | Live update   | Condition                 | Description
:--                                             | :---      | :------           | :----------   | :----------               | :----------
`agent.nic_config`                              | bool      | `false`           | no            | virtual machine           | Set the name and MTU of the default network interfaces to be the same as the instance devices (this is automatic for containers).
`boot.autostart`                                | bool      | -                 | no            | -                         | Always start the instance when LXD starts (if not set, restore last state)
`boot.autostart.delay`                          | integer   | `0`               | no            | -                         | Number of seconds to wait after the instance started before starting the next one
`boot.autostart.priority`                       | integer   | `0`               | no            | -                         | What order to start the instances in (starting with highest)
`boot.host_shutdown_timeout`                    | integer   | `30`              | yes           | -                         | Seconds to wait for instance to shutdown before it is force stopped
`boot.stop.priority`                            | integer   | `0`               | no            | -                         | What order to shutdown the instances (starting with highest)
`cloud-init.network-config`                     | string    | `DHCP on eth0`    | no            | -                         | Cloud-init `network-config`, content is used as seed value
`cloud-init.user-data`                          | string    | `#cloud-config`   | no            | -                         | Cloud-init `user-data`, content is used as seed value
`cloud-init.vendor-data`                        | string    | `#cloud-config`   | no            | -                         | Cloud-init `vendor-data`, content is used as seed value
`cluster.evacuate`                              | string    | `auto`            | no            | -                         | What to do when evacuating the instance (`auto`, `migrate`, `live-migrate`, or `stop`)
`environment.*`                                 | string    | -                 | yes (exec)    | -                         | key/value environment variables to export to the instance and set on exec
`limits.cpu`                                    | string    | -                 | yes           | -                         | Number or range of CPUs to expose to the instance (defaults to 1 CPU for VMs)
`limits.cpu.allowance`                          | string    | `100%`            | yes           | container                 | How much of the CPU can be used. Can be a percentage (e.g. 50%) for a soft limit or hard a chunk of time (25ms/100ms)
`limits.cpu.priority`                           | integer   | `10` (maximum)    | yes           | container                 | CPU scheduling priority compared to other instances sharing the same CPUs (overcommit) (integer between 0 and 10)
`limits.disk.priority`                          | integer   | `5` (medium)      | yes           | -                         | When under load, how much priority to give to the instance's I/O requests (integer between 0 and 10)
`limits.hugepages.64KB`                         | string    | -                 | yes           | container                 | Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 64 KB huge pages (Available huge-page sizes are architecture dependent.)
`limits.hugepages.1MB`                          | string    | -                 | yes           | container                 | Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 1 MB huge pages (Available huge-page sizes are architecture dependent.)
`limits.hugepages.2MB`                          | string    | -                 | yes           | container                 | Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 2 MB huge pages (Available huge-page sizes are architecture dependent.)
`limits.hugepages.1GB`                          | string    | -                 | yes           | container                 | Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 1 GB huge pages (Available huge-page sizes are architecture dependent.)
`limits.kernel.*`                               | string    | -                 | no            | container                 | This limits kernel resources per instance (e.g. number of open files)
`limits.memory`                                 | string    | -                 | yes           | -                         | Percentage of the host's memory or fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) (defaults to 1GiB for VMs)
`limits.memory.enforce`                         | string    | `hard`            | yes           | container                 | If `hard`, instance can't exceed its memory limit. If `soft`, the instance can exceed its memory limit when extra host memory is available
`limits.memory.hugepages`                       | bool      | `false`           | no            | virtual machine           | Controls whether to back the instance using huge pages rather than regular system memory
`limits.memory.swap`                            | bool      | `true`            | yes           | container                 | Controls whether to encourage/discourage swapping less used pages for this instance
`limits.memory.swap.priority`                   | integer   | `10` (maximum)    | yes           | container                 | The higher this is set, the least likely the instance is to be swapped to disk (integer between 0 and 10)
`limits.network.priority`                       | integer   | `0` (minimum)     | yes           | -                         | When under load, how much priority to give to the instance's network requests (integer between 0 and 10)
`limits.processes`                              | integer   | - (max)           | yes           | container                 | Maximum number of processes that can run in the instance
`linux.kernel_modules`                          | string    | -                 | yes           | container                 | Comma-separated list of kernel modules to load before starting the instance
`linux.sysctl.*`                                | string    | -                 | no            | container                 | Allow for modify `sysctl` settings
`migration.incremental.memory`                  | bool      | `false`           | yes           | container                 | Incremental memory transfer of the instance's memory to reduce downtime
`migration.incremental.memory.goal`             | integer   | `70`              | yes           | container                 | Percentage of memory to have in sync before stopping the instance
`migration.incremental.memory.iterations`       | integer   | `10`              | yes           | container                 | Maximum number of transfer operations to go through before stopping the instance
`migration.stateful`                            | bool      | `false`           | no            | virtual machine           | Allow for stateful stop/start and snapshots. This will prevent the use of some features that are incompatible with it
`nvidia.driver.capabilities`                    | string    | `compute,utility` | no            | container                 | What driver capabilities the instance needs (sets `libnvidia-container` `NVIDIA_DRIVER_CAPABILITIES`)
`nvidia.runtime`                                | bool      | `false`           | no            | container                 | Pass the host NVIDIA and CUDA runtime libraries into the instance
`nvidia.require.cuda`                           | string    | -                 | no            | container                 | Version expression for the required CUDA version (sets `libnvidia-container` `NVIDIA_REQUIRE_CUDA`)
`nvidia.require.driver`                         | string    | -                 | no            | container                 | Version expression for the required driver version (sets `libnvidia-container` `NVIDIA_REQUIRE_DRIVER`)
`raw.apparmor`                                  | blob      | -                 | yes           | -                         | AppArmor profile entries to be appended to the generated profile
`raw.idmap`                                     | blob      | -                 | no            | unprivileged container    | Raw idmap configuration (e.g. `both 1000 1000`)
`raw.lxc`                                       | blob      | -                 | no            | container                 | Raw LXC configuration to be appended to the generated one
`raw.qemu`                                      | blob      | -                 | no            | virtual machine           | Raw QEMU configuration to be appended to the generated command line
`raw.qemu.conf`                                 | blob      | -                 | no            | virtual machine           | Addition/override to the generated `qemu.conf` file
`raw.seccomp`                                   | blob      | -                 | no            | container                 | Raw Seccomp configuration
`security.devlxd`                               | bool      | `true`            | no            | -                         | Controls the presence of `/dev/lxd` in the instance
`security.devlxd.images`                        | bool      | `false`           | no            | container                 | Controls the availability of the `/1.0/images` API over `devlxd`
`security.idmap.base`                           | integer   | -                 | no            | unprivileged container    | The base host ID to use for the allocation (overrides auto-detection)
`security.idmap.isolated`                       | bool      | `false`           | no            | unprivileged container    | Use an idmap for this instance that is unique among instances with isolated set
`security.idmap.size`                           | integer   | -                 | no            | unprivileged container    | The size of the idmap to use
`security.nesting`                              | bool      | `false`           | yes           | container                 | Support running LXD (nested) inside the instance
`security.privileged`                           | bool      | `false`           | no            | container                 | Runs the instance in privileged mode
`security.protection.delete`                    | bool      | `false`           | yes           | -                         | Prevents the instance from being deleted
`security.protection.shift`                     | bool      | `false`           | yes           | container                 | Prevents the instance's file system from being UID/GID shifted on startup
`security.agent.metrics`                        | bool      | `true`            | no            | virtual machine           | Controls whether the `lxd-agent` is queried for state information and metrics
`security.secureboot`                           | bool      | `true`            | no            | virtual machine           | Controls whether UEFI secure boot is enabled with the default Microsoft keys
`security.syscalls.allow`                       | string    | -                 | no            | container                 | A '\n' separated list of syscalls to allow (mutually exclusive with `security.syscalls.deny*`)
`security.syscalls.deny`                        | string    | -                 | no            | container                 | A '\n' separated list of syscalls to deny
`security.syscalls.deny_compat`                 | bool      | `false`           | no            | container                 | On x86_64 this enables blocking of `compat_*` syscalls, it is a no-op on other arches
`security.syscalls.deny_default`                | bool      | `true`            | no            | container                 | Enables the default syscall deny
`security.syscalls.intercept.bpf`               | bool      | `false`           | no            | container                 | Handles the `bpf` system call
`security.syscalls.intercept.bpf.devices`       | bool      | `false`           | no            | container                 | Allows `bpf` programs for the devices cgroup in the unified hierarchy to be loaded.
`security.syscalls.intercept.mknod`             | bool      | `false`           | no            | container                 | Handles the `mknod` and `mknodat` system calls (allows creation of a limited subset of char/block devices)
`security.syscalls.intercept.mount`             | bool      | `false`           | no            | container                 | Handles the `mount` system call
`security.syscalls.intercept.mount.allowed`     | string    | -                 | yes           | container                 | Specify a comma-separated list of file systems that are safe to mount for processes inside the instance
`security.syscalls.intercept.mount.fuse`        | string    | -                 | yes           | container                 | Whether to redirect mounts of a given file system to their FUSE implementation (e.g. `ext4=fuse2fs`)
`security.syscalls.intercept.mount.shift`       | bool      | `false`           | yes           | container                 | Whether to mount `shiftfs` on top of file systems handled through mount syscall interception
`security.syscalls.intercept.sched_setscheduler`| bool      | `false`           | no            | container                 | Handles the `sched_setscheduler` system call (allows increasing process priority)
`security.syscalls.intercept.setxattr`          | bool      | `false`           | no            | container                 | Handles the `setxattr` system call (allows setting a limited subset of restricted extended attributes)
`security.syscalls.intercept.sysinfo`           | bool      | `false`           | no            | container                 | Handles the `sysinfo` system call (to get cgroup-based resource usage information)
`snapshots.schedule`                            | string    | -                 | no            | -                         | Cron expression (`<minute> <hour> <dom> <month> <dow>`), or a comma-separated list of schedule aliases `<@hourly> <@daily> <@midnight> <@weekly> <@monthly> <@annually> <@yearly> <@startup> <@never>`
`snapshots.schedule.stopped`                    | bool      | `false`           | no            | -                         | Controls whether to automatically snapshot stopped instances
`snapshots.pattern`                             | string    | `snap%d`          | no            | -                         | Pongo2 template string which represents the snapshot name (used for scheduled snapshots and unnamed snapshots)
`snapshots.expiry`                              | string    | -                 | no            | -                         | Controls when snapshots are to be deleted (expects expression like `1M 2H 3d 4w 5m 6y`)
`user.*`                                        | string    | -                 | no            | -                         | Free form user key/value storage (can be used in search)

The following volatile keys are currently internally used by LXD:

Key                                         | Type      | Default       | Description
:--                                         | :---      | :------       | :----------
`volatile.apply_template`                   | string    | -             | The name of a template hook which should be triggered upon next startup
`volatile.apply_nvram`                      | string    | -             | Whether or not to regenerate VM NVRAM on next start
`volatile.base_image`                       | string    | -             | The hash of the image the instance was created from, if any
`volatile.cloud-init.instance-id`           | string    | -             | The `instance-id` (UUID) exposed to cloud-init
`volatile.evacuate.origin`                  | string    | -             | The origin (cluster member) of the evacuated instance
`volatile.idmap.base`                       | integer   | -             | The first ID in the instance's primary idmap range
`volatile.idmap.current`                    | string    | -             | The idmap currently in use by the instance
`volatile.idmap.next`                       | string    | -             | The idmap to use next time the instance starts
`volatile.last_state.idmap`                 | string    | -             | Serialized instance UID/GID map
`volatile.last_state.power`                 | string    | -             | Instance state as of last host shutdown
`volatile.vsock_id`                         | string    | -             | Instance `vsock` ID used as of last start
`volatile.uuid`                             | string    | -             | Instance UUID (globally unique across all servers and projects)
`volatile.<name>.apply_quota`               | string    | -             | Disk quota to be applied on next instance start
`volatile.<name>.ceph_rbd`                  | string    | -             | RBD device path for Ceph disk devices
`volatile.<name>.host_name`                 | string    | -             | Network device name on the host
`volatile.<name>.hwaddr`                    | string    | -             | Network device MAC address (when no `hwaddr` property is set on the device itself)
`volatile.<name>.last_state.created`        | string    | -             | Whether or not the network device physical device was created (`true` or `false`)
`volatile.<name>.last_state.mtu`            | string    | -             | Network device original MTU used when moving a physical device into an instance
`volatile.<name>.last_state.hwaddr`         | string    | -             | Network device original MAC used when moving a physical device into an instance
`volatile.<name>.last_state.vf.id`          | string    | -             | SR-IOV Virtual function ID used when moving a VF into an instance
`volatile.<name>.last_state.vf.hwaddr`      | string    | -             | SR-IOV Virtual function original MAC used when moving a VF into an instance
`volatile.<name>.last_state.vf.vlan`        | string    | -             | SR-IOV Virtual function original VLAN used when moving a VF into an instance
`volatile.<name>.last_state.vf.spoofcheck`  | string    | -             | SR-IOV Virtual function original spoof check setting used when moving a VF into an instance

Additionally, those user keys have become common with images (support isn't guaranteed):

Key                         | Type          | Default           | Description
:--                         | :---          | :------           | :----------
`user.meta-data`            | string        | -                 | Cloud-init meta-data, content is appended to seed value

Note that while a type is defined above as a convenience, all values are
stored as strings and should be exported over the REST API as strings
(which makes it possible to support any extra values without breaking
backward compatibility).

Those keys can be set using the `lxc` tool with:

```bash
lxc config set <instance> <key> <value>
```

Volatile keys can't be set by the user and can only be set directly against an instance.

The raw keys allow direct interaction with the backend features that LXD
itself uses, setting those may very well break LXD in non-obvious ways
and should whenever possible be avoided.

## CPU limits

The CPU limits are implemented through a mix of the `cpuset` and `cpu` cgroup controllers.

`limits.cpu` results in CPU pinning through the `cpuset` controller.
A set of CPUs (e.g. `1,2,3`) or a CPU range (e.g. `0-3`) can be specified.

When a number of CPUs is specified instead (e.g. `4`), LXD will do
dynamic load-balancing of all instances that aren't pinned to specific
CPUs, trying to spread the load on the machine. Instances will then be
re-balanced every time an instance starts or stops as well as whenever a
CPU is added to the system.

To pin to a single CPU, you have to use the range syntax (e.g. `1-1`) to
differentiate it from a number of CPUs.

`limits.cpu.allowance` drives either the CFS scheduler quotas when
passed a time constraint, or the generic CPU shares mechanism when
passed a percentage value.

The time constraint (e.g. `20ms/50ms`) is relative to one CPU worth of
time, so to restrict to two CPUs worth of time, something like
100ms/50ms should be used.

When using a percentage value, the limit will only be applied when under
load and will be used to calculate the scheduler priority for the
instance, relative to any other instance which is using the same CPU(s).

`limits.cpu.priority` is another knob which is used to compute that
scheduler priority score when a number of instances sharing a set of
CPUs have the same percentage of CPU assigned to them.

## VM CPU topology

LXD virtual machines default to having just one vCPU allocated which
shows up as matching the host CPU vendor and type but has a single core
and no threads.

When `limits.cpu` is set to a single integer, this will cause multiple
vCPUs to be allocated and exposed to the guest as full cores. Those vCPUs
will not be pinned to specific physical cores on the host.
The number of vCPUs can be updated while the VM is running.

When `limits.cpu` is set to a range or comma-separated list of CPU IDs
(as provided by `lxc info --resources`), then the vCPUs will be pinned
to those physical cores. In this scenario, LXD will check whether the
CPU configuration lines up with a realistic hardware topology and if it
does, it will replicate that topology in the guest.
When doing CPU pinning, it is not possible to change the configuration while the VM is running.

This means that if the pinning configuration includes 8 threads, with
each pair of thread coming from the same core and an even number of
cores spread across two CPUs, LXD will have the guest show two CPUs,
each with two cores and each core with two threads. The NUMA layout is
similarly replicated and in this scenario, the guest would most likely
end up with two NUMA nodes, one for each CPU socket.

In such an environment with multiple NUMA nodes, the memory will
similarly be divided across NUMA nodes and be pinned accordingly on the
host and then exposed to the guest.

All this allows for very high performance operations in the guest as the
guest scheduler can properly reason about sockets, cores and threads as
well as consider NUMA topology when sharing memory or moving processes
across NUMA nodes.

## Huge page limits via `limits.hugepages.[size]`

LXD allows to limit the number of huge pages available to a container through
the `limits.hugepage.[size]` key. Limiting huge pages is done through the
`hugetlb` cgroup controller. This means the host system needs to expose the
`hugetlb` controller in the legacy or unified cgroup hierarchy for these limits
to apply.
Note that architectures often expose multiple huge-page sizes. In addition,
architectures may expose different huge-page sizes than other architectures.

Limiting huge pages is especially useful when LXD is configured to intercept the
`mount` syscall for the `hugetlbfs` file system in unprivileged containers. When
LXD intercepts a `hugetlbfs` `mount`  syscall, it will mount the `hugetlbfs`
file system for a container with correct `uid` and `gid` values as `mount`
options. This makes it possible to use huge pages from unprivileged containers.
However, it is recommended to limit the number of huge pages available to the
container through `limits.hugepages.[size]` to stop the container from being
able to exhaust the huge pages available to the host.

## Resource limits via `limits.kernel.[limit name]`

LXD exposes a generic namespaced key `limits.kernel.*` which can be used to set
resource limits for a given instance. It is generic in the sense that LXD will
not perform any validation on the resource that is specified following the
`limits.kernel.*` prefix. LXD cannot know about all the possible resources that
a given kernel supports. Instead, LXD will simply pass down the corresponding
resource key after the `limits.kernel.*` prefix and its value to the kernel.
The kernel will do the appropriate validation. This allows users to specify any
supported limit on their system. Some common limits are:

Key                       | Resource          | Description
:--                       | :---              | :----------
`limits.kernel.as`        | `RLIMIT_AS`       | Maximum size of the process's virtual memory
`limits.kernel.core`      | `RLIMIT_CORE`     | Maximum size of the process's coredump file
`limits.kernel.cpu`       | `RLIMIT_CPU`      | Limit in seconds on the amount of CPU time the process can consume
`limits.kernel.data`      | `RLIMIT_DATA`     | Maximum size of the process's data segment
`limits.kernel.fsize`     | `RLIMIT_FSIZE`    | Maximum size of files the process may create
`limits.kernel.locks`     | `RLIMIT_LOCKS`    | Limit on the number of file locks that this process may establish
`limits.kernel.memlock`   | `RLIMIT_MEMLOCK`  | Limit on the number of bytes of memory that the process may lock in RAM
`limits.kernel.nice`      | `RLIMIT_NICE`     | Maximum value to which the process's nice value can be raised
`limits.kernel.nofile`    | `RLIMIT_NOFILE`   | Maximum number of open files for the process
`limits.kernel.nproc`     | `RLIMIT_NPROC`    | Maximum number of processes that can be created for the user of the calling process
`limits.kernel.rtprio`    | `RLIMIT_RTPRIO`   | Maximum value on the real-time-priority that maybe set for this process
`limits.kernel.sigpending`| `RLIMIT_SIGPENDING` | Maximum number of signals that maybe queued for the user of the calling process

A full list of all available limits can be found in the manpages for the
`getrlimit(2)`/`setrlimit(2)` system calls. To specify a limit within the
`limits.kernel.*` namespace use the resource name in lowercase without the
`RLIMIT_` prefix, e.g.  `RLIMIT_NOFILE` should be specified as `nofile`.
A limit is specified as two colon separated values which are either numeric or
the word `unlimited` (e.g. `limits.kernel.nofile=1000:2000`). A single value can be
used as a shortcut to set both soft and hard limit (e.g.
`limits.kernel.nofile=3000`) to the same value. A resource with no explicitly
configured limitation will be inherited from the process starting up the
instance. Note that this inheritance is not enforced by LXD but by the kernel.

## Snapshot scheduling and configuration

LXD supports scheduled snapshots which can be created at most once every minute.
There are three configuration options:

- `snapshots.schedule` takes a shortened cron expression: `<minute> <hour> <day-of-month> <month> <day-of-week>`.
  If this is empty (default), no snapshots will be created.
- `snapshots.schedule.stopped` controls whether to automatically snapshot stopped instances.
  It defaults to `false`.
- `snapshots.pattern` takes a Pongo2 template string to format the snapshot name.
  To name snapshots with time stamps, the Pongo2 context variable `creation_date` can be used.
  Be aware that you should format the date (e.g. use `{{ creation_date|date:"2006-01-02_15-04-05" }}`) in your template string to avoid forbidden characters in the snapshot name.
  Another way to avoid name collisions is to use the placeholder `%d`.
  If a snapshot with the same name (excluding the placeholder) already exists, all existing snapshot names will be taken into account to find the highest number at the placeholders position.
  This number will be incremented by one for the new name. The starting number if no snapshot exists will be `0`.
  The default behavior of `snapshots.pattern` is equivalent to a format string of `snap%d`.

Example of using Pongo2 syntax to format snapshot names with timestamps:

```bash
lxc config set INSTANCE snapshots.pattern "{{ creation_date|date:'2006-01-02_15-04-05' }}"
```

This results in snapshots named `{date/time of creation}` down to the precision of a second.
