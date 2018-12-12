# Container configuration
## Properties
The following are direct container properties and can't be part of a profile:

 - `name`
 - `architecture`

Name is the container name and can only be changed by renaming the container.

Valid container names must:

 - Be between 1 and 63 characters long
 - Be made up exclusively of letters, numbers and dashes from the ASCII table
 - Not start with a digit or a dash
 - Not end with a dash

This requirement is so that the container name may properly be used in
DNS records, on the filesystem, in various security profiles as well as
the hostname of the container itself.

## Key/value configuration
The key/value configuration is namespaced with the following namespaces
currently supported:

 - `boot` (boot related options, timing, dependencies, ...)
 - `environment` (environment variables)
 - `image` (copy of the image properties at time of creation)
 - `limits` (resource limits)
 - `nvidia` (NVIDIA and CUDA configuration)
 - `raw` (raw container configuration overrides)
 - `security` (security policies)
 - `user` (storage for user properties, searchable)
 - `volatile` (used internally by LXD to store settings that are specific to a specific container instance)

The currently supported keys are:

Key                                     | Type      | Default           | Live update   | API extension                        | Description
:--                                     | :---      | :------           | :----------   | :------------                        | :----------
boot.autostart                          | boolean   | -                 | n/a           | -                                    | Always start the container when LXD starts (if not set, restore last state)
boot.autostart.delay                    | integer   | 0                 | n/a           | -                                    | Number of seconds to wait after the container started before starting the next one
boot.autostart.priority                 | integer   | 0                 | n/a           | -                                    | What order to start the containers in (starting with highest)
boot.host\_shutdown\_timeout            | integer   | 30                | yes           | container\_host\_shutdown\_timeout   | Seconds to wait for container to shutdown before it is force stopped
boot.stop.priority                      | integer   | 0                 | n/a           | container\_stop\_priority            | What order to shutdown the containers (starting with highest)
environment.\*                          | string    | -                 | yes (exec)    | -                                    | key/value environment variables to export to the container and set on exec
limits.cpu                              | string    | - (all)           | yes           | -                                    | Number or range of CPUs to expose to the container
limits.cpu.allowance                    | string    | 100%              | yes           | -                                    | How much of the CPU can be used. Can be a percentage (e.g. 50%) for a soft limit or hard a chunk of time (25ms/100ms)
limits.cpu.priority                     | integer   | 10 (maximum)      | yes           | -                                    | CPU scheduling priority compared to other containers sharing the same CPUs (overcommit) (integer between 0 and 10)
limits.disk.priority                    | integer   | 5 (medium)        | yes           | -                                    | When under load, how much priority to give to the container's I/O requests (integer between 0 and 10)
limits.kernel.\*                        | string    | -                 | no            | kernel\_limits                       | This limits kernel resources per container (e.g. number of open files)
limits.memory                           | string    | - (all)           | yes           | -                                    | Percentage of the host's memory or fixed value in bytes (supports kB, MB, GB, TB, PB and EB suffixes)
limits.memory.enforce                   | string    | hard              | yes           | -                                    | If hard, container can't exceed its memory limit. If soft, the container can exceed its memory limit when extra host memory is available.
limits.memory.swap                      | boolean   | true              | yes           | -                                    | Whether to allow some of the container's memory to be swapped out to disk
limits.memory.swap.priority             | integer   | 10 (maximum)      | yes           | -                                    | The higher this is set, the least likely the container is to be swapped to disk (integer between 0 and 10)
limits.network.priority                 | integer   | 0 (minimum)       | yes           | -                                    | When under load, how much priority to give to the container's network requests (integer between 0 and 10)
limits.processes                        | integer   | - (max)           | yes           | -                                    | Maximum number of processes that can run in the container
linux.kernel\_modules                   | string    | -                 | yes           | -                                    | Comma separated list of kernel modules to load before starting the container
migration.incremental.memory            | boolean   | false             | yes           | migration\_pre\_copy                 | Incremental memory transfer of the container's memory to reduce downtime.
migration.incremental.memory.goal       | integer   | 70                | yes           | migration\_pre\_copy                 | Percentage of memory to have in sync before stopping the container.
migration.incremental.memory.iterations | integer   | 10                | yes           | migration\_pre\_copy                 | Maximum number of transfer operations to go through before stopping the container.
nvidia.driver.capabilities              | string    | compute,utility   | no            | nvidia\_runtime\_config              | What driver capabilities the container needs (sets libnvidia-container NVIDIA\_DRIVER\_CAPABILITIES)
nvidia.runtime                          | boolean   | false             | no            | nvidia\_runtime                      | Pass the host NVIDIA and CUDA runtime libraries into the container
nvidia.require.cuda                     | string    | -                 | no            | nvidia\_runtime\_config              | Version expression for the required CUDA version (sets libnvidia-container NVIDIA\_REQUIRE\_CUDA)
nvidia.require.driver                   | string    | -                 | no            | nvidia\_runtime\_config              | Version expression for the required driver version (sets libnvidia-container NVIDIA\_REQUIRE\_DRIVER)
raw.apparmor                            | blob      | -                 | yes           | -                                    | Apparmor profile entries to be appended to the generated profile
raw.idmap                               | blob      | -                 | no            | id\_map                              | Raw idmap configuration (e.g. "both 1000 1000")
raw.lxc                                 | blob      | -                 | no            | -                                    | Raw LXC configuration to be appended to the generated one
raw.seccomp                             | blob      | -                 | no            | container\_syscall\_filtering        | Raw Seccomp configuration
security.devlxd                         | boolean   | true              | no            | restrict\_devlxd                     | Controls the presence of /dev/lxd in the container
security.devlxd.images                  | boolean   | false             | no            | devlxd\_images                       | Controls the availability of the /1.0/images API over devlxd
security.idmap.base                     | integer   | -                 | no            | id\_map\_base                        | The base host ID to use for the allocation (overrides auto-detection)
security.idmap.isolated                 | boolean   | false             | no            | id\_map                              | Use an idmap for this container that is unique among containers with isolated set.
security.idmap.size                     | integer   | -                 | no            | id\_map                              | The size of the idmap to use
security.nesting                        | boolean   | false             | yes           | -                                    | Support running lxd (nested) inside the container
security.privileged                     | boolean   | false             | no            | -                                    | Runs the container in privileged mode
security.protection.delete              | boolean   | false             | yes           | container\_protection\_delete        | Prevents the container from being deleted
security.protection.shift               | boolean   | false             | yes           | container\_protection\_shift         | Prevents the container's filesystem from being uid/gid shifted on startup
security.syscalls.blacklist             | string    | -                 | no            | container\_syscall\_filtering        | A '\n' separated list of syscalls to blacklist
security.syscalls.blacklist\_compat     | boolean   | false             | no            | container\_syscall\_filtering        | On x86\_64 this enables blocking of compat\_\* syscalls, it is a no-op on other arches
security.syscalls.blacklist\_default    | boolean   | true              | no            | container\_syscall\_filtering        | Enables the default syscall blacklist
security.syscalls.whitelist             | string    | -                 | no            | container\_syscall\_filtering        | A '\n' separated list of syscalls to whitelist (mutually exclusive with security.syscalls.blacklist\*)
snapshots.schedule                      | string    | -                 | no            | snapshot\_scheduling                 | Cron expression (`<minute> <hour> <dom> <month> <dow>`)
snapshots.schedule.stopped              | bool      | false             | no            | snapshot\_scheduling                 | Controls whether or not stopped containers are to be snapshoted automatically
snapshots.pattern                       | string    | snap%d            | no            | snapshot\_scheduling                 | Pongo2 template string which represents the snapshot name (used for scheduled snapshots and unnamed snapshots)
user.\*                                 | string    | -                 | n/a           | -                                    | Free form user key/value storage (can be used in search)

The following volatile keys are currently internally used by LXD:

Key                             | Type      | Default       | Description
:--                             | :---      | :------       | :----------
volatile.apply\_quota           | string    | -             | Disk quota to be applied on next container start
volatile.apply\_template        | string    | -             | The name of a template hook which should be triggered upon next startup
volatile.base\_image            | string    | -             | The hash of the image the container was created from, if any.
volatile.idmap.base             | integer   | -             | The first id in the container's primary idmap range
volatile.idmap.next             | string    | -             | The idmap to use next time the container starts
volatile.last\_state.idmap      | string    | -             | Serialized container uid/gid map
volatile.last\_state.power      | string    | -             | Container state as of last host shutdown
volatile.\<name\>.host\_name    | string    | -             | Network device name on the host (for nictype=bridged or nictype=p2p, or nictype=sriov)
volatile.\<name\>.hwaddr        | string    | -             | Network device MAC address (when no hwaddr property is set on the device itself)
volatile.\<name\>.name          | string    | -             | Network device name (when no name propery is set on the device itself)


Additionally, those user keys have become common with images (support isn't guaranteed):

Key                         | Type          | Default           | Description
:--                         | :---          | :------           | :----------
user.meta-data              | string        | -                 | Cloud-init meta-data, content is appended to seed value.
user.network-config         | string        | DHCP on eth0      | Cloud-init network-config, content is used as seed value.
user.network\_mode          | string        | dhcp              | One of "dhcp" or "link-local". Used to configure network in supported images.
user.user-data              | string        | #!cloud-config    | Cloud-init user-data, content is used as seed value.
user.vendor-data            | string        | #!cloud-config    | Cloud-init vendor-data, content is used as seed value.

Note that while a type is defined above as a convenience, all values are
stored as strings and should be exported over the REST API as strings
(which makes it possible to support any extra values without breaking
backward compatibility).

Those keys can be set using the lxc tool with:

```bash
lxc config set <container> <key> <value>
```

Volatile keys can't be set by the user and can only be set directly against a container.

The raw keys allow direct interaction with the backend features that LXD
itself uses, setting those may very well break LXD in non-obvious ways
and should whenever possible be avoided.

### CPU limits
The CPU limits are implemented through a mix of the `cpuset` and `cpu` CGroup controllers.

`limits.cpu` results in CPU pinning through the `cpuset` controller.
A set of CPUs (e.g. `1,2,3`) or a CPU range (e.g. `0-3`) can be specified.

When a number of CPUs is specified instead (e.g. `4`), LXD will do
dynamic load-balancing of all containers that aren't pinned to specific
CPUs, trying to spread the load on the machine. Containers will then be
re-balanced every time a container starts or stops as well as whenever a
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
container, relative to any other container which is using the same CPU(s).

`limits.cpu.priority` is another knob which is used to compute that
scheduler priority score when a number of containers sharing a set of
CPUs have the same percentage of CPU assigned to them.

# Devices configuration
LXD will always provide the container with the basic devices which are required
for a standard POSIX system to work. These aren't visible in container or
profile configuration and may not be overridden.

Those includes:

 - `/dev/null` (character device)
 - `/dev/zero` (character device)
 - `/dev/full` (character device)
 - `/dev/console` (character device)
 - `/dev/tty` (character device)
 - `/dev/random` (character device)
 - `/dev/urandom` (character device)
 - `/dev/net/tun` (character device)
 - `/dev/fuse` (character device)
 - `lo` (network interface)

Anything else has to be defined in the container configuration or in one of its
profiles. The default profile will typically contain a network interface to
become `eth0` in the container.

To add extra devices to a container, device entries can be added directly to a
container, or to a profile.

Devices may be added or removed while the container is running.

Every device entry is identified by a unique name. If the same name is used in
a subsequent profile or in the container's own configuration, the whole entry
is overridden by the new definition.

Device entries are added to a container through:

```bash
lxc config device add <container> <name> <type> [key=value]...
```

or to a profile with:

```bash
lxc profile device add <profile> <name> <type> [key=value]...
```

## Device types
LXD supports the following device types:

ID (database)   | Name                              | Description
:--             | :--                               | :--
0               | [none](#type-none)                | Inheritance blocker
1               | [nic](#type-nic)                  | Network interface
2               | [disk](#type-disk)                | Mountpoint inside the container
3               | [unix-char](#type-unix-char)      | Unix character device
4               | [unix-block](#type-unix-block)    | Unix block device
5               | [usb](#type-usb)                  | USB device
6               | [gpu](#type-gpu)                  | GPU device
7               | [infiniband](#type-infiniband)    | Infiniband device
8               | [proxy](#type-proxy)              | Proxy device

### Type: none
A none type device doesn't have any property and doesn't create anything inside the container.

It's only purpose it to stop inheritance of devices coming from profiles.

To do so, just add a none type device with the same name of the one you wish to skip inheriting.
It can be added in a profile being applied after the profile it originated from or directly on the container.

### Type: nic
LXD supports different kind of network devices:

 - `physical`: Straight physical device passthrough from the host. The targeted device will vanish from the host and appear in the container.
 - `bridged`: Uses an existing bridge on the host and creates a virtual device pair to connect the host bridge to the container.
 - `macvlan`: Sets up a new network device based on an existing one but using a different MAC address.
 - `p2p`: Creates a virtual device pair, putting one side in the container and leaving the other side on the host.
 - `sriov`: Passes a virtual function of an SR-IOV enabled physical network device into the container.

Different network interface types have different additional properties, the current list is:

Key                     | Type      | Default           | Required  | Used by                           | API extension                          | Description
:--                     | :--       | :--               | :--       | :--                               | :--                                    | :--
nictype                 | string    | -                 | yes       | all                               | -                                      | The device type, one of "bridged", "macvlan", "p2p", "physical", or "sriov"
limits.ingress          | string    | -                 | no        | bridged, p2p                      | -                                      | I/O limit in bit/s for incoming traffic (supports kbit, Mbit, Gbit suffixes)
limits.egress           | string    | -                 | no        | bridged, p2p                      | -                                      | I/O limit in bit/s for outgoing traffic (supports kbit, Mbit, Gbit suffixes)
limits.max              | string    | -                 | no        | bridged, p2p                      | -                                      | Same as modifying both limits.ingress and limits.egress
name                    | string    | kernel assigned   | no        | all                               | -                                      | The name of the interface inside the container
host\_name              | string    | randomly assigned | no        | bridged, macvlan, p2p, sriov      | -                                      | The name of the interface inside the host
hwaddr                  | string    | randomly assigned | no        | all                               | -                                      | The MAC address of the new interface
mtu                     | integer   | parent MTU        | no        | all                               | -                                      | The MTU of the new interface
parent                  | string    | -                 | yes       | bridged, macvlan, physical, sriov | -                                      | The name of the host device or bridge
vlan                    | integer   | -                 | no        | macvlan, physical                 | network\_vlan, network\_vlan\_physical | The VLAN ID to attach to
ipv4.address            | string    | -                 | no        | bridged                           | network                                | An IPv4 address to assign to the container through DHCP
ipv6.address            | string    | -                 | no        | bridged                           | network                                | An IPv6 address to assign to the container through DHCP
security.mac\_filtering | boolean   | false             | no        | bridged                           | network                                | Prevent the container from spoofing another's MAC address
maas.subnet.ipv4        | string    | -                 | no        | bridged, macvlan, physical, sriov | maas\_network                          | MAAS IPv4 subnet to register the container in
maas.subnet.ipv6        | string    | -                 | no        | bridged, macvlan, physical, sriov | maas\_network                          | MAAS IPv6 subnet to register the container in

#### bridged or macvlan for connection to physical network
The `bridged` and `macvlan` interface types can both be used to connect
to an existing physical network.

macvlan effectively lets you fork your physical NIC, getting a second
interface that's then used by the container. This saves you from
creating a bridge device and veth pairs and usually offers better
performance than a bridge.

The downside to this is that macvlan devices while able to communicate
between themselves and to the outside, aren't able to talk to their
parent device. This means that you can't use macvlan if you ever need
your containers to talk to the host itself.

In such case, a bridge is preferable. A bridge will also let you use mac
filtering and I/O limits which cannot be applied to a macvlan device.

#### SR-IOV
The `sriov` interface type supports SR-IOV enabled network devices. These
devices associate a set of virtual functions (VFs) with the single physical
function (PF) of the network device. PFs are standard PCIe functions. VFs on
the other hand are very lightweight PCIe functions that are optimized for data
movement. They come with a limited set of configuration capabilities to prevent
changing properties of the PF. Given that VFs appear as regular PCIe devices to
the system they can be passed to containers just like a regular physical
device. The `sriov` interface type expects to be passed the name of an SR-IOV
enabled network device on the system via the `parent` property. LXD will then
check for any available VFs on the system. By default LXD will allocate the
first free VF it finds. If it detects that either none are enabled or all
currently enabled VFs are in use it will bump the number of supported VFs to
the maximum value and use the first free VF. If all possible VFs are in use or
the kernel or card doesn't support incrementing the number of VFs LXD will
return an error. To create a `sriov` network device use:

```
lxc config device add <container> <device-name> nic nictype=sriov parent=<sriov-enabled-device>
```

To tell LXD to use a specific unused VF add the `host_name` property and pass
it the name of the enabled VF.


#### MAAS integration
If you're using MAAS to manage the physical network under your LXD host
and want to attach your containers directly to a MAAS managed network,
LXD can be configured to interact with MAAS so that it can track your
containers.

At the daemon level, you must configure `maas.api.url` and
`maas.api.key`, then set the `maas.subnet.ipv4` and/or
`maas.subnet.ipv6` keys on the container or profile's `nic` entry.

This will have LXD register all your containers with MAAS, giving them
proper DHCP leases and DNS records.

If you set the `ipv4.address` or `ipv6.address` keys on the nic, then
those will be registered as static assignments in MAAS too.

### Type: infiniband
LXD supports two different kind of network types for infiniband devices:

 - `physical`: Straight physical device passthrough from the host. The targeted device will vanish from the host and appear in the container.
 - `sriov`: Passes a virtual function of an SR-IOV enabled physical network device into the container.

Different network interface types have different additional properties, the current list is:

Key                     | Type      | Default           | Required  | Used by         | API extension | Description
:--                     | :--       | :--               | :--       | :--             | :--           | :--
nictype                 | string    | -                 | yes       | all             | infiniband    | The device type, one of "physical", or "sriov"
name                    | string    | kernel assigned   | no        | all             | infiniband    | The name of the interface inside the container
hwaddr                  | string    | randomly assigned | no        | all             | infiniband    | The MAC address of the new interface
mtu                     | integer   | parent MTU        | no        | all             | infiniband    | The MTU of the new interface
parent                  | string    | -                 | yes       | physical, sriov | infiniband    | The name of the host device or bridge

To create a `physical` `infiniband` device use:

```
lxc config device add <container> <device-name> infiniband nictype=physical parent=<device>
```

#### SR-IOV with infiniband devices
Infiniband devices do support SR-IOV but in contrast to other SR-IOV enabled
devices infiniband does not support dynamic device creation in SR-IOV mode.
This means users need to pre-configure the number of virtual functions by
configuring the corresponding kernel module.

To create a `sriov` `infiniband` device use:

```
lxc config device add <container> <device-name> infiniband nictype=sriov parent=<sriov-enabled-device>
```

### Type: disk
Disk entries are essentially mountpoints inside the container. They can
either be a bind-mount of an existing file or directory on the host, or
if the source is a block device, a regular mount.

The following properties exist:

Key             | Type      | Default           | Required  | Description
:--             | :--       | :--               | :--       | :--
limits.read     | string    | -                 | no        | I/O limit in byte/s (supports kB, MB, GB, TB, PB and EB suffixes) or in iops (must be suffixed with "iops")
limits.write    | string    | -                 | no        | I/O limit in byte/s (supports kB, MB, GB, TB, PB and EB suffixes) or in iops (must be suffixed with "iops")
limits.max      | string    | -                 | no        | Same as modifying both limits.read and limits.write
path            | string    | -                 | yes       | Path inside the container where the disk will be mounted
source          | string    | -                 | yes       | Path on the host, either to a file/directory or to a block device
optional        | boolean   | false             | no        | Controls whether to fail if the source doesn't exist
readonly        | boolean   | false             | no        | Controls whether to make the mount read-only
size            | string    | -                 | no        | Disk size in bytes (supports kB, MB, GB, TB, PB and EB suffixes). This is only supported for the rootfs (/).
recursive       | boolean   | false             | no        | Whether or not to recursively mount the source path
pool            | string    | -                 | no        | The storage pool the disk device belongs to. This is only applicable for storage volumes managed by LXD.
propagation     | string    | -                 | no        | Controls how a bind-mount is shared between the container and the host. (Can be one of `private`, the default, or `shared`, `slave`, `unbindable`,  `rshared`, `rslave`, `runbindable`,  `rprivate`. Please see the Linux Kernel [shared subtree](https://www.kernel.org/doc/Documentation/filesystems/sharedsubtree.txt) documentation for a full explanation)

If multiple disks, backed by the same block device, have I/O limits set,
the average of the limits will be used.

### Type: unix-char
Unix character device entries simply make the requested character device
appear in the container's `/dev` and allow read/write operations to it.

The following properties exist:

Key         | Type      | Default           | API extension                     | Required  | Description
:--         | :--       | :--               | :--                               | :--       | :--
source      | string    | -                 | unix\_device\_rename              | no        | Path on the host
path        | string    | -                 |                                   | no        | Path inside the container(one of "source" and "path" must be set)
major       | int       | device on host    |                                   | no        | Device major number
minor       | int       | device on host    |                                   | no        | Device minor number
uid         | int       | 0                 |                                   | no        | UID of the device owner in the container
gid         | int       | 0                 |                                   | no        | GID of the device owner in the container
mode        | int       | 0660              |                                   | no        | Mode of the device in the container
required    | boolean   | true              | unix\_device\_hotplug             | no        | Whether or not this device is required to start the container.

### Type: unix-block
Unix block device entries simply make the requested block device
appear in the container's `/dev` and allow read/write operations to it.

The following properties exist:

Key         | Type      | Default           | API extension                     | Required  | Description
:--         | :--       | :--               | :--                               | :--       | :--
source      | string    | -                 | unix\_device\_rename              | no        | Path on the host
path        | string    | -                 |                                   | no        | Path inside the container(one of "source" and "path" must be set)
major       | int       | device on host    |                                   | no        | Device major number
minor       | int       | device on host    |                                   | no        | Device minor number
uid         | int       | 0                 |                                   | no        | UID of the device owner in the container
gid         | int       | 0                 |                                   | no        | GID of the device owner in the container
mode        | int       | 0660              |                                   | no        | Mode of the device in the container
required    | boolean   | true              | unix\_device\_hotplug             | no        | Whether or not this device is required to start the container.

### Type: usb
USB device entries simply make the requested USB device appear in the
container.

The following properties exist:

Key         | Type      | Default           | Required  | Description
:--         | :--       | :--               | :--       | :--
vendorid    | string    | -                 | no        | The vendor id of the USB device.
productid   | string    | -                 | no        | The product id of the USB device.
uid         | int       | 0                 | no        | UID of the device owner in the container
gid         | int       | 0                 | no        | GID of the device owner in the container
mode        | int       | 0660              | no        | Mode of the device in the container
required    | boolean   | false             | no        | Whether or not this device is required to start the container. (The default is no, and all devices are hot-pluggable.)

### Type: gpu
GPU device entries simply make the requested gpu device appear in the
container.

The following properties exist:

Key         | Type      | Default           | Required  | Description
:--         | :--       | :--               | :--       | :--
vendorid    | string    | -                 | no        | The vendor id of the GPU device.
productid   | string    | -                 | no        | The product id of the GPU device.
id          | string    | -                 | no        | The card id of the GPU device.
pci         | string    | -                 | no        | The pci address of the GPU device.
uid         | int       | 0                 | no        | UID of the device owner in the container
gid         | int       | 0                 | no        | GID of the device owner in the container
mode        | int       | 0660              | no        | Mode of the device in the container

### Type: proxy
Proxy devices allow forwarding network connections between host and container.
This makes it possible to forward traffic hitting one of the host's
addresses to an address inside the container or to do the reverse and
have an address in the container connect through the host.

The supported connection types are:
* `TCP <-> TCP`
* `UDP <-> UDP`
* `UNIX <-> UNIX`
* `TCP <-> UNIX`
* `UNIX <-> TCP`
* `UDP <-> TCP`
* `TCP <-> UDP`
* `UDP <-> UNIX`
* `UNIX <-> UDP`

Key             | Type      | Default           | Required  | Description
:--             | :--       | :--               | :--       | :--
listen          | string    | -                 | yes       | The address and port to bind and listen
connect         | string    | -                 | yes       | The address and port to connect to
bind            | string    | host              | no        | Which side to bind on (host/container)
uid             | int       | 0                 | no        | UID of the owner of the listening Unix socket
gid             | int       | 0                 | no        | GID of the owner of the listening Unix socket
mode            | int       | 0755              | no        | Mode for the listening Unix socket
nat             | bool      | false             | no        | Whether to optimize proxying via NAT
proxy\_protocol | bool      | false             | no        | Whether to use the HAProxy PROXY protocol to transmit sender information
security.uid    | int       | 0                 | no        | What UID to drop privilege to
security.gid    | int       | 0                 | no        | What GID to drop privilege to

```
lxc config device add <container> <device-name> proxy listen=<type>:<addr>:<port>[-<port>][,<port>] connect=<type>:<addr>:<port> bind=<host/container>
```

## Instance types
LXD supports simple instance types. Those are represented as a string
which can be passed at container creation time.

There are three allowed syntaxes:

 - `<instance type>`
 - `<cloud>:<instance type>`
 - `c<CPU>-m<RAM in GB>`

For example, those 3 are equivalent:

 - t2.micro
 - aws:t2.micro
 - c1-m1

On the command line, this is passed like this:

```bash
lxc launch ubuntu:16.04 my-container -t t2.micro
```

The list of supported clouds and instance types can be found here:

  https://github.com/dustinkirkland/instance-type

## Resource limits via `limits.kernel.[limit name]`
LXD exposes a generic namespaced key `limits.kernel.*` which can be used to set
resource limits for a given container. It is generic in the sense that LXD will
not perform any validation on the resource that is specified following the
`limits.kernel.*` prefix. LXD cannot know about all the possible resources that
a given kernel supports. Instead, LXD will simply pass down the corresponding
resource key after the `limits.kernel.*` prefix and its value to the kernel.
The kernel will do the appropriate validation. This allows users to specify any
supported limit on their system. Some common limits are:

Key                      | Resource          | Description
:--                      | :---              | :----------
limits.kernel.as         | RLIMIT\_AS         | Maximum size of the process's virtual memory
limits.kernel.core       | RLIMIT\_CORE       | Maximum size of the process's coredump file
limits.kernel.cpu        | RLIMIT\_CPU        | Limit in seconds on the amount of cpu time the process can consume
limits.kernel.data       | RLIMIT\_DATA       | Maximum size of the process's data segment
limits.kernel.fsize      | RLIMIT\_FSIZE      | Maximum size of files the process may create
limits.kernel.locks      | RLIMIT\_LOCKS      | Limit on the number of file locks that this process may establish
limits.kernel.memlock    | RLIMIT\_MEMLOCK    | Limit on the number of bytes of memory that the process may lock in RAM
limits.kernel.nice       | RLIMIT\_NICE       | Maximum value to which the process's nice value can be raised
limits.kernel.nofile     | RLIMIT\_NOFILE     | Maximum number of open files for the process
limits.kernel.nproc      | RLIMIT\_NPROC      | Maximum number of processes that can be created for the user of the calling process
limits.kernel.rtprio     | RLIMIT\_RTPRIO     | Maximum value on the real-time-priority that maybe set for this process
limits.kernel.sigpending | RLIMIT\_SIGPENDING | Maximum number of signals that maybe queued for the user of the calling process

A full list of all available limits can be found in the manpages for the
`getrlimit(2)`/`setrlimit(2)` system calls. To specify a limit within the
`limits.kernel.*` namespace use the resource name in lowercase without the
`RLIMIT_` prefix, e.g.  `RLIMIT_NOFILE` should be specified as `nofile`.
A limit is specified as two colon separated values which are either numeric or
the word `unlimited` (e.g. `limits.kernel.nofile=1000:2000`). A single value can be
used as a shortcut to set both soft and hard limit (e.g.
`limits.kernel.nofile=3000`) to the same value. A resource with no explicitly
configured limitation will be inherited from the process starting up the
container. Note that this inheritance is not enforced by LXD but by the kernel.

## Live migration
LXD supports live migration of containers using [CRIU](http://criu.org). In
order to optimize the memory transfer for a container LXD can be instructed to
make use of CRIU's pre-copy features by setting the
`migration.incremental.memory` property to `true`. This means LXD will request
CRIU to perform a series of memory dumps for the container. After each dump LXD
will send the memory dump to the specified remote. In an ideal scenario each
memory dump will decrease the delta to the previous memory dump thereby
increasing the percentage of memory that is already synced. When the percentage
of synced memory is equal to or greater than the threshold specified via
`migration.incremental.memory.goal` LXD will request CRIU to perform a final
memory dump and transfer it. If the threshold is not reached after the maximum
number of allowed iterations specified via
`migration.incremental.memory.iterations` LXD will request a final memory dump
from CRIU and migrate the container.

## Snapshot scheduling
LXD supports scheduled snapshots which can be created at most once every minute.
There are three configuration options. `snapshots.schedule` takes a shortened
cron expression: `<minute> <hour> <day-of-month> <month> <day-of-week>`. If this is
empty (default), no snapshots will be created. `snapshots.schedule.stopped`
controls whether or not stopped container are to be automatically snapshotted.
It defaults to `false`. `snapshots.pattern` takes a pongo2 template string,
and the pongo2 context contains the `creation_date` variable. In order to avoid
name colisions, snapshots will be suffixed with `-0`, `-1`, and so on.
