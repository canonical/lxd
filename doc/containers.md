# Container configuration
## Properties
The following are direct container properties and can't be part of a profile:

 - `name`
 - `architecture`

Name is the container name and can only be changed by renaming the container.

## Key/value configuration
The key/value configuration is namespaced with the following namespaces
currently supported:

 - `boot` (boot related options, timing, dependencies, ...)
 - `environment` (environment variables)
 - `limits` (resource limits)
 - `raw` (raw container configuration overrides)
 - `security` (security policies)
 - `user` (storage for user properties, searchable)
 - `volatile` (used internally by LXD to store settings that are specific to a specific container instance)

The currently supported keys are:

Key                         | Type      | Default       | Live update   | Description
:--                         | :---      | :------       | :----------   | :----------
boot.autostart              | boolean   | -             | n/a           | Always start the container when LXD starts (if not set, restore last state)
boot.autostart.delay        | integer   | 0             | n/a           | Number of seconds to wait after the container started before starting the next one
boot.autostart.priority     | integer   | 0             | n/a           | What order to start the containers in (starting with highest)
environment.\*              | string    | -             | yes (exec)    | key/value environment variables to export to the container and set on exec
limits.cpu                  | string    | - (all)       | yes           | Number or range of CPUs to expose to the container
limits.cpu.allowance        | string    | 100%          | yes           | How much of the CPU can be used. Can be a percentage (e.g. 50%) for a soft limit or hard a chunk of time (25ms/100ms)
limits.cpu.priority         | integer   | 10 (maximum)  | yes           | CPU scheduling priority compared to other containers sharing the same CPUs (overcommit) (integer between 0 and 10)
limits.disk.priority        | integer   | 5 (medium)    | yes           | When under load, how much priority to give to the container's I/O requests (integer between 0 and 10)
limits.memory               | string    | - (all)       | yes           | Percentage of the host's memory or fixed value in bytes (supports kB, MB, GB, TB, PB and EB suffixes)
limits.memory.enforce       | string    | hard          | yes           | If hard, container can't exceed its memory limit. If soft, the container can exceed its memory limit when extra host memory is available.
limits.memory.swap          | boolean   | true          | yes           | Whether to allow some of the container's memory to be swapped out to disk
limits.memory.swap.priority | integer   | 10 (maximum)  | yes           | The higher this is set, the least likely the container is to be swapped to disk (integer between 0 and 10)
limits.network.priority     | integer   | 0 (minimum)   | yes           | When under load, how much priority to give to the container's network requests (integer between 0 and 10)
limits.processes            | integer   | - (max)       | yes           | Maximum number of processes that can run in the container
linux.kernel\_modules       | string    | -             | yes           | Comma separated list of kernel modules to load before starting the container
raw.apparmor                | blob      | -             | yes           | Apparmor profile entries to be appended to the generated profile
raw.idmap                   | blob      | -             | no            | Raw idmap configuration (e.g. "both 1000 1000")
raw.lxc                     | blob      | -             | no            | Raw LXC configuration to be appended to the generated one
security.idmap.base         | integer   | -             | no            | The base host ID to use for the allocation (overrides auto-detection)
security.idmap.isolated     | boolean   | false         | no            | Use an idmap for this container that is unique among containers with isolated set.
security.idmap.size         | integer   | -             | no            | The size of the idmap to use
security.nesting            | boolean   | false         | yes           | Support running lxd (nested) inside the container
security.privileged         | boolean   | false         | no            | Runs the container in privileged mode
user.\*                     | string    | -             | n/a           | Free form user key/value storage (can be used in search)

The following volatile keys are currently internally used by LXD:

Key                             | Type      | Default       | Description
:--                             | :---      | :------       | :----------
volatile.apply\_template        | string    | -             | The name of a template hook which should be triggered upon next startup
volatile.base\_image            | string    | -             | The hash of the image the container was created from, if any.
volatile.idmap.base             | integer   | -             | The first id in the container's primary idmap range
volatile.idmap.next             | string    | -             | The idmap to use next time the container starts
volatile.last\_state.idmap      | string    | -             | Serialized container uid/gid map
volatile.last\_state.power      | string    | -             | Container state as of last host shutdown
volatile.\<name\>.host\_name    | string    | -             | Network device name on the host (for nictype=bridged or nictype=p2p)
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


## Devices configuration
LXD will always provide the container with the basic devices which are
required for a standard POSIX system to work. These aren't visible in
container or profile configuration and may not be overriden.

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

Every device entry is identified by a unique name. If the same name is
used in a subsequent profile or in the container's own configuration,
the whole entry is overriden by the new definition.

Device entries are added through:

```bash
lxc config device add <container> <name> <type> [key=value]...
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

Different network interface types have different additional properties, the current list is:

Key             | Type      | Default           | Required  | Used by                       | Description
:--             | :--       | :--               | :--       | :--                           | :--
nictype         | string    | -                 | yes       | all                           | The device type, one of "physical", "bridged", "macvlan" or "p2p"
limits.ingress  | string    | -                 | no        | bridged, p2p                  | I/O limit in bit/s (supports kbit, Mbit, Gbit suffixes)
limits.egress   | string    | -                 | no        | bridged, p2p                  | I/O limit in bit/s (supports kbit, Mbit, Gbit suffixes)
limits.max      | string    | -                 | no        | bridged, p2p                  | Same as modifying both limits.read and limits.write
name            | string    | kernel assigned   | no        | all                           | The name of the interface inside the container
host\_name      | string    | randomly assigned | no        | bridged, p2p, macvlan         | The name of the interface inside the host
hwaddr          | string    | randomly assigned | no        | all                           | The MAC address of the new interface
mtu             | integer   | parent MTU        | no        | all                           | The MTU of the new interface
parent          | string    | -                 | yes       | physical, bridged, macvlan    | The name of the host device or bridge

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

If multiple disks, backed by the same block device, have I/O limits set,
the average of the limits will be used.

### Type: unix-char
Unix character device entries simply make the requested character device
appear in the container's `/dev` and allow read/write operations to it.

The following properties exist:

Key         | Type      | Default           | Required  | Description
:--         | :--       | :--               | :--       | :--
path        | string    | -                 | yes       | Path inside the container
major       | int       | device on host    | no        | Device major number
minor       | int       | device on host    | no        | Device minor number
uid         | int       | 0                 | no        | UID of the device owner in the container
gid         | int       | 0                 | no        | GID of the device owner in the container
mode        | int       | 0660              | no        | Mode of the device in the container

### Type: unix-block
Unix block device entries simply make the requested block device
appear in the container's /dev and allow read/write operations to it.

The following properties exist:

Key         | Type      | Default           | Required  | Description
:--         | :--       | :--               | :--       | :--
path        | string    | -                 | yes       | Path inside the container
major       | int       | device on host    | no        | Device major number
minor       | int       | device on host    | no        | Device minor number
uid         | int       | 0                 | no        | UID of the device owner in the container
gid         | int       | 0                 | no        | GID of the device owner in the container
mode        | int       | 0660              | no        | Mode of the device in the container

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
