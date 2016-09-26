# Introduction
Current LXD stores the following kind of configurations:
 - Server configuration (the LXD daemon itself)
 - Container configuration

The server configuration is a simple set of key and values.

The container configuration is a bit more complex as it uses both
key/value configuration and some more complex configuration structures
for devices, network interfaces and storage volumes.

# Server configuration
## Key/value configuration
The key/value configuration is namespaced with the following namespaces
currently supported:
 - core (core daemon configuration)
 - images (image configuration)
 - storage (storage configuration)

Key                             | Type      | Default   | API extension                     | Description
:--                             | :---      | :------   | :------------                     | :----------
core.https\_address             | string    | -         | -                                 | Address to bind for the remote API
core.https\_allowed\_origin     | string    | -         | -                                 | Access-Control-Allow-Origin http header value
core.https\_allowed\_methods    | string    | -         | -                                 | Access-Control-Allow-Methods http header value
core.https\_allowed\_headers    | string    | -         | -                                 | Access-Control-Allow-Headers http header value
core.https\_allowed\_credentials| boolean   | -         | -                                 | Whether to set Access-Control-Allow-Credentials http header value to "true"
core.proxy\_https               | string    | -         | -                                 | https proxy to use, if any (falls back to HTTPS\_PROXY environment variable)
core.proxy\_http                | string    | -         | -                                 | http proxy to use, if any (falls back to HTTP\_PROXY environment variable)
core.proxy\_ignore\_hosts       | string    | -         | -                                 | hosts which don't need the proxy for use (similar format to NO\_PROXY, e.g. 1.2.3.4,1.2.3.5, falls back to NO\_PROXY environment variable)
core.trust\_password            | string    | -         | -                                 | Password to be provided by clients to setup a trust
storage.lvm\_vg\_name           | string    | -         | -                                 | LVM Volume Group name to be used for container and image storage. A default Thin Pool is created using 100% of the free space in the Volume Group, unless `storage.lvm_thinpool_name` is set.
storage.lvm\_thinpool\_name     | string    | "LXDPool" | -                                 | LVM Thin Pool to use within the Volume Group specified in `storage.lvm_vg_name`, if the default pool parameters are undesirable.
storage.lvm\_fstype             | string    | ext4      | -                                 | Format LV with filesystem, for now it's value can be only ext4 (default) or xfs.
storage.lvm\_mount\_options     | string    | discard   | storage\_lvm\_mount\_options      | Mount options for the LV filesystem
storage.lvm\_volume\_size       | string    | 10GiB     | -                                 | Size of the logical volume
storage.zfs\_pool\_name         | string    | -         | -                                 | ZFS pool name
storage.zfs\_remove\_snapshots  | boolean   | false     | storage\_zfs\_remove\_snapshots   | Automatically remove any needed snapshot when attempting a container restore
storage.zfs\_use\_refquota      | boolean   | false     | storage\_zfs\_use\_refquota       | Don't include snapshots as part of container quota (size property) or in reported disk usage
images.compression\_algorithm   | string    | gzip      | -                                 | Compression algorithm to use for new images (bzip2, gzip, lzma, xz or none)
images.remote\_cache\_expiry    | integer   | 10        | -                                 | Number of days after which an unused cached remote image will be flushed
images.auto\_update\_interval   | integer   | 6         | -                                 | Interval in hours at which to look for update to cached images (0 disables it)
images.auto\_update\_cached     | boolean   | true      | -                                 | Whether to automatically update any image that LXD caches

Those keys can be set using the lxc tool with:

    lxc config set <key> <value>


# Container configuration
## Properties
The following are direct container properties and can't be part of a profile:
 - name
 - architecture

Name is the container name and can only be changed by renaming the container.

## Key/value configuration
The key/value configuration is namespaced with the following namespaces
currently supported:
 - boot (boot related options, timing, dependencies, ...)
 - environment (environment variables)
 - limits (resource limits)
 - raw (raw container configuration overrides)
 - security (security policies)
 - user (storage for user properties, searchable)
 - volatile (used internally by LXD to store settings that are specific to a specific container instance)

The currently supported keys are:

Key                                  | Type      | Default       | Live update   | API extension                        | Description
:--                                  | :---      | :------       | :----------   | :------------                        | :----------
boot.autostart                       | boolean   | false         | n/a           | -                                    | Always start the container when LXD starts
boot.autostart.delay                 | integer   | 0             | n/a           | -                                    | Number of seconds to wait after the container started before starting the next one
boot.autostart.priority              | integer   | 0             | n/a           | -                                    | What order to start the containers in (starting with highest)
boot.host\_shutdown\_timeout         | integer   | 30            | yes           | container\_host\_shutdown\_timeout   | Seconds to wait for container to shutdown before it is force stopped
environment.\*                       | string    | -             | yes (exec)    | -                                    | key/value environment variables to export to the container and set on exec
limits.cpu                           | string    | - (all)       | yes           | -                                    | Number or range of CPUs to expose to the container
limits.cpu.allowance                 | string    | 100%          | yes           | -                                    | How much of the CPU can be used. Can be a percentage (e.g. 50%) for a soft limit or hard a chunk of time (25ms/100ms)
limits.cpu.priority                  | integer   | 10 (maximum)  | yes           | -                                    | CPU scheduling priority compared to other containers sharing the same CPUs (overcommit) (integer between 0 and 10)
limits.disk.priority                 | integer   | 5 (medium)    | yes           | -                                    | When under load, how much priority to give to the container's I/O requests (integer between 0 and 10)
limits.memory                        | string    | - (all)       | yes           | -                                    | Percentage of the host's memory or fixed value in bytes (supports kB, MB, GB, TB, PB and EB suffixes)
limits.memory.enforce                | string    | hard          | yes           | -                                    | If hard, container can't exceed its memory limit. If soft, the container can exceed its memory limit when extra host memory is available.
limits.memory.swap                   | boolean   | true          | yes           | -                                    | Whether to allow some of the container's memory to be swapped out to disk
limits.memory.swap.priority          | integer   | 10 (maximum)  | yes           | -                                    | The higher this is set, the least likely the container is to be swapped to disk (integer between 0 and 10)
limits.network.priority              | integer   | 0 (minimum)   | yes           | -                                    | When under load, how much priority to give to the container's network requests (integer between 0 and 10)
limits.processes                     | integer   | - (max)       | yes           | -                                    | Maximum number of processes that can run in the container
linux.kernel\_modules                | string    | -             | yes           | -                                    | Comma separated list of kernel modules to load before starting the container
raw.apparmor                         | blob      | -             | yes           | -                                    | Apparmor profile entries to be appended to the generated profile
raw.lxc                              | blob      | -             | no            | -                                    | Raw LXC configuration to be appended to the generated one
raw.seccomp                          | blob      | -             | no            | container\_syscall\_filtering        | Raw Seccomp configuration
security.nesting                     | boolean   | false         | yes           | -                                    | Support running lxd (nested) inside the container
security.privileged                  | boolean   | false         | no            | -                                    | Runs the container in privileged mode
security.syscalls.blacklist\_default | boolean   | true          | no            | container\_syscall\_filtering        | Enables the default syscall blacklist
security.syscalls.blacklist\_compat  | boolean   | false         | no            | container\_syscall\_filtering        | On x86\_64 this enables blocking of compat\_\* syscalls, it is a no-op on other arches
security.syscalls.blacklist          | string    | -             | no            | container\_syscall\_filtering        | A '\n' separated list of syscalls to blacklist
security.syscalls.whitelist          | string    | -             | no            | container\_syscall\_filtering        | A '\n' separated list of syscalls to whitelist (mutually exclusive with security.syscalls.blacklist\*)
user.\*                              | string    | -             | n/a           | -                                    |Free form user key/value storage (can be used in search)

The following volatile keys are currently internally used by LXD:

Key                         | Type      | Default       | Description
:--                         | :---      | :------       | :----------
volatile.\<name\>.hwaddr    | string    | -             | Network device MAC address (when no hwaddr property is set on the device itself)
volatile.\<name\>.name      | string    | -             | Network device name (when no name propery is set on the device itself)
volatile.apply\_template    | string    | -             | The name of a template hook which should be triggered upon next startup
volatile.base\_image        | string    | -             | The hash of the image the container was created from, if any.
volatile.last\_state.idmap  | string    | -             | Serialized container uid/gid map
volatile.last\_state.power  | string    | -             | Container state as of last host shutdown


Additionally, those user keys have become common with images (support isn't guaranteed):

Key                         | Type          | Default           | Description
:--                         | :---          | :------           | :----------
user.network\_mode          | string        | dhcp              | One of "dhcp" or "link-local". Used to configure network in supported images.
user.meta-data              | string        | -                 | Cloud-init meta-data, content is appended to seed value.
user.user-data              | string        | #!cloud-config    | Cloud-init user-data, content is used as seed value.
user.vendor-data            | string        | #!cloud-config    | Cloud-init vendor-data, content is used as seed value.

Note that while a type is defined above as a convenience, all values are
stored as strings and should be exported over the REST API as strings
(which makes it possible to support any extra values without breaking
backward compatibility).

Those keys can be set using the lxc tool with:

    lxc config set <container> <key> <value>

Volatile keys can't be set by the user and can only be set directly against a container.

The raw keys allow direct interaction with the backend features that LXD
itself uses, setting those may very well break LXD in non-obvious ways
and should whenever possible be avoided.


## Devices configuration
LXD will always provide the container with the basic devices which are
required for a standard POSIX system to work. These aren't visible in
container or profile configuration and may not be overriden.

Those includes:
 - /dev/null (character device)
 - /dev/zero (character device)
 - /dev/full (character device)
 - /dev/console (character device)
 - /dev/tty (character device)
 - /dev/random (character device)
 - /dev/urandom (character device)
 - /dev/net/tun (character device)
 - /dev/fuse (character device)
 - lo (network interface)

Anything else has to be defined in the container configuration or in one
of its profiles. The default profile will typically contain a network
interface to become eth0 in the container.

To add extra devices to a container, device entries can be added
directly to a container, or to a profile.

Devices may be added or removed while the container is running.

Every device entry is identified by a unique name. If the same name is
used in a subsequent profile or in the container's own configuration,
the whole entry is overriden by the new definition.

Device entries are added through:
    lxc config device add <container> <name> <type> [key=value]...
    lxc profile device add <profile> <name> <type> [key=value]...

### Device types
LXD supports the following device types:

ID (database)   | Name          | Description
:--             | :--           | :--
0               | none          | Inheritance blocker
1               | nic           | Network interface
2               | disk          | Mountpoint inside the container
3               | unix-char     | Unix character device
4               | unix-block    | Unix block device

### Type: none
A none type device doesn't have any property and doesn't create anything inside the container.

It's only purpose it to stop inheritance of devices coming from profiles.

To do so, just add a none type device with the same name of the one you wish to skip inheriting.
It can be added in a profile being applied after the profile it originated from or directly on the container.

### Type: nic
LXD supports different kind of network devices:
 - physical: Straight physical device passthrough from the host. The targeted device will vanish from the host and appear in the container.
 - bridged: Uses an existing bridge on the host and creates a virtual device pair to connect the host bridge to the container.
 - macvlan: Sets up a new network device based on an existing one but using a different MAC address.
 - p2p: Creates a virtual device pair, putting one side in the container and leaving the other side on the host.

Different network interface types have different additional properties, the current list is:

Key                     | Type      | Default           | Required  | Used by                       | API extension | Description
:--                     | :--       | :--               | :--       | :--                           | :--           | :--
nictype                 | string    | -                 | yes       | all                           | -             | The device type, one of "physical", "bridged", "macvlan" or "p2p"
limits.ingress          | string    | -                 | no        | bridged, p2p                  | -             | I/O limit in bit/s (supports kbit, Mbit, Gbit suffixes)
limits.egress           | string    | -                 | no        | bridged, p2p                  | -             | I/O limit in bit/s (supports kbit, Mbit, Gbit suffixes)
limits.max              | string    | -                 | no        | bridged, p2p                  | -             | Same as modifying both limits.read and limits.write
name                    | string    | kernel assigned   | no        | all                           | -             | The name of the interface inside the container
host\_name              | string    | randomly assigned | no        | bridged, p2p, macvlan         | -             | The name of the interface inside the host
hwaddr                  | string    | randomly assigned | no        | all                           | -             | The MAC address of the new interface
mtu                     | integer   | parent MTU        | no        | all                           | -             | The MTU of the new interface
parent                  | string    | -                 | yes       | physical, bridged, macvlan    | -             | The name of the host device or bridge
ipv4.address            | string    | -                 | no        | bridged                       | network       | An IPv4 address to assign to the container through DHCP
ipv6.address            | string    | -                 | no        | bridged                       | network       | An IPv6 address to assign to the container through DHCP
security.mac\_filtering | boolean   | false             | no        | bridged                       | network       | Prevent the container from spoofing another's MAC address

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

### Type: usb
USB device entries simply make the requested USB device appear in the
container.

The following properties exist:

Key         | Type      | Default           | Required  | Description
:--         | :--       | :--               | :--       | :--
productid   | string    | -                 | yes       | The product id of the USB device.
vendorid    | string    | -                 | no        | The vendor id of the USB device.
uid         | int       | 0                 | no        | UID of the device owner in the container
gid         | int       | 0                 | no        | GID of the device owner in the container
mode        | int       | 0660              | no        | Mode of the device in the container
required    | boolean   | false             | no        | Whether or not this device is required to start the container. (The default is no, and all devices are hot-pluggable.)

## Profiles
Profiles can store any configuration that a container can (key/value or devices)
and any number of profiles can be applied to a container.

Profiles are applied in the order they are specified so the last profile
to specify a specific key wins.

In any case, resource-specific configuration always overrides that
coming from the profiles.


If not present, LXD will create a "default" profile.

The "default" profile is set for any new container created which doesn't
specify a different profiles list.

# Network configuration
LXD supports creating and managing bridges, below is a list of the
configuration options supported for those bridges.

Note that this feature was introduced as part of API extension "network".

The key/value configuration is namespaced with the following namespaces
currently supported:
 - bridge (L2 interface configuration)
 - fan (configuration specific to the Ubuntu FAN overlay)
 - tunnel (cross-host tunneling configuration)
 - ipv4 (L3 IPv4 configuration)
 - ipv6 (L3 IPv6 configuration)
 - dns (DNS server and resolution configuration)
 - raw (raw configuration file content)
 - user (free form key/value for user metadata)

It is expected that IP addresses and subnets are given using CIDR notation (1.1.1.1/24 or fd80:1234::1/64).
The exception being tunnel local and remote addresses which are just plain addresses (1.1.1.1 or fd80:1234::1).

Key                             | Type      | Condition             | Default                   | Description
:--                             | :--       | :--                   | :--                       | :--
bridge.driver                   | string    | -                     | native                    | Bridge driver ("native" or "openvswitch")
bridge.external\_interfaces     | string    | -                     | -                         | Comma separate list of unconfigured network interfaces to include in the bridge
bridge.mtu                      | integer   | -                     | 1500                      | Bridge MTU (default varies if tunnel or fan setup)
bridge.mode                     | string    | -                     | standard                  | Bridge operation mode ("standard" or "fan")
fan.underlay\_subnet            | string    | fan mode              | default gateway subnet    | Subnet to use as the underlay for the FAN (CIDR notation)
fan.overlay\_subnet             | string    | fan mode              | 240.0.0.0/8               | Subnet to use as the overlay for the FAN (CIDR notation)
fan.type                        | string    | fan mode              | vxlan                     | The tunneling type for the FAN ("vxlan" or "ipip")
tunnel.NAME.protocol            | string    | standard mode         | -                         | Tunneling protocol ("vxlan" or "gre")
tunnel.NAME.local               | string    | gre or vxlan          | -                         | Local address for the tunnel (not necessary for multicast vxlan)
tunnel.NAME.remote              | string    | gre or vxlan          | -                         | Remote address for the tunnel (not necessary for multicast vxlan)
tunnel.NAME.group               | string    | vxlan                 | 239.0.0.1                 | Multicast address for vxlan (used if local and remote aren't set)
tunnel.NAME.port                | integer   | vxlan                 | 0                         | Specific port to use for the vxlan tunnel
tunnel.NAME.id                  | integer   | vxlan                 | 0                         | Specific tunnel ID to use for the vxlan tunnel
ipv4.address                    | string    | standard mode         | random unused subnet      | IPv4 address for the bridge (CIDR notation). Use "none" to turn off IPv4 or "auto" to generate a new one
ipv4.nat                        | boolean   | ipv4 address          | false                     | Whether to NAT (will default to true if unset and a random ipv4.address is generated)
ipv4.dhcp                       | boolean   | ipv4 address          | true                      | Whether to allocate addresses using DHCP
ipv4.dhcp.ranges                | string    | ipv4 dhcp             | all addresses             | Comma separated list of IP ranges to use for DHCP (FIRST-LAST format)
ipv4.routing                    | boolean   | ipv4 address          | true                      | Whether to route traffic in and out of the bridge
ipv6.address                    | string    | standard mode         | random unused subnet      | IPv6 address for the bridge (CIDR notation). Use "none" to turn off IPv6 or "auto" to generate a new one
ipv6.nat                        | boolean   | ipv6 address          | false                     | Whether to NAT (will default to true if unset and a random ipv6.address is generated)
ipv6.dhcp                       | boolean   | ipv6 address          | true                      | Whether to provide additional network configuration over DHCP
ipv6.dhcp.stateful              | boolean   | ipv6 dhcp             | false                     | Whether to allocate addresses using DHCP
ipv6.dhcp.ranges                | string    | ipv6 stateful dhcp    | all addresses             | Comma separated list of IPv6 ranges to use for DHCP (FIRST-LAST format)
ipv6.routing                    | boolean   | ipv6 address          | true                      | Whether to route traffic in and out of the bridge
dns.domain                      | string    | -                     | lxd                       | Domain to advertise to DHCP clients and use for DNS resolution
dns.mode                        | string    | -                     | managed                   | DNS registration mode ("none" for no DNS record, "managed" for LXD generated static records or "dynamic" for client generated records)
raw.dnsmasq                     | string    | -                     | -                         | Additional dnsmasq configuration to append to the configuration


Those keys can be set using the lxc tool with:

    lxc network set <network> <key> <value>
