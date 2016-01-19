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

Key                             | Type          | Default                   | Description
:--                             | :---          | :------                   | :----------
core.https\_address             | string        | -                         | Address to bind for the remote API
core.https\_allowed\_origin     | string        | -                         | Access-Control-Allow-Origin http header value
core.trust\_password            | string        | -                         | Password to be provided by clients to setup a trust
storage.lvm\_vg\_name           | string        | -                         | LVM Volume Group name to be used for container and image storage. A default Thin Pool is created using 100% of the free space in the Volume Group, unless `storage.lvm_thinpool_name` is set.
storage.lvm\_thinpool\_name     | string        | "LXDPool"                 | LVM Thin Pool to use within the Volume Group specified in `storage.lvm_vg_name`, if the default pool parameters are undesirable.
storage.lvm\_fstype             | string        | ext4                      | Format LV with filesystem, for now it's value can be only ext4 (default) or xfs.
storage.zfs\_pool\_name         | string        | -                         | ZFS pool name
images.compression\_algorithm   | string        | gzip                      | Compression algorithm to use for new images (bzip2, gzip, lzma, xz or none)
images.remote\_cache\_expiry    | integer       | 10                        | Number of days after which an unused cached remote image will be flushed

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

Key                         | Type      | Default       | Live update   | Description
:--                         | :---      | :------       | :----------   | :----------
boot.autostart              | boolean   | false         | n/a           | Always start the container when LXD starts
boot.autostart.delay        | integer   | 0             | n/a           | Number of seconds to wait after the container started before starting the next one
boot.autostart.priority     | integer   | 0             | n/a           | What order to start the containers in (starting with highest)
environment.\*              | string    | -             | yes (exec)    | key/value environment variables to export to the container and set on exec
limits.cpu                  | string    | - (all)       | yes           | Number or range of CPUs to expose to the container
limits.cpu.allowance        | string    | 100%          | yes           | How much of the CPU can be used. Can be a percentage (e.g. 50%) for a soft limit or hard a chunk of time (25ms/100ms)
limits.cpu.priority         | integer   | 10 (maximum)  | yes           | CPU scheduling priority compared to other containers sharing the same CPUs (overcommit)
limits.memory               | string    | - (all)       | yes           | Percentage of the host's memory or fixed value in bytes (supports kB, MB, GB, TB, PB and EB suffixes)
limits.memory.enforce       | string    | hard          | yes           | If hard, container can't exceed its memory limit. If soft, the container can exceed its memory limit when extra host memory is available.
limits.memory.swap          | boolean   | true          | yes           | Whether to allow some of the container's memory to be swapped out to disk
limits.memory.swap.priority | integer   | 10 (maximum)  | yes           | The higher this is set, the least likely the container is to be swapped to disk
linux.kernel\_modules       | string    | -             | yes           | Comma separated list of kernel modules to load before starting the container
raw.apparmor                | blob      | -             | yes           | Apparmor profile entries to be appended to the generated profile
raw.lxc                     | blob      | -             | no            | Raw LXC configuration to be appended to the generated one
security.nesting            | boolean   | false         | yes           | Support running lxd (nested) inside the container
security.privileged         | boolean   | false         | no            | Runs the container in privileged mode
user.\*                     | string    | -             | n/a           | Free form user key/value storage (can be used in search)

The following volatile keys are currently internally used by LXD:

Key                         | Type      | Default       | Description
:--                         | :---      | :------       | :----------
volatile.\<name\>.hwaddr    | string    | -             | Network device MAC address (when no hwaddr property is set on the device itself)
volatile.\<name\>.name      | string    | -             | Network device name (when no name propery is set on the device itself)
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

Key        | Type      | Default           | Required  | Used by                       | Description
:--        | :--       | :--               | :--       | :--                           | :--
nictype    | string    | -                 | yes       | all                           | The device type, one of "physical", "bridged", "macvlan" or "p2p"
name       | string    | kernel assigned   | no        | all                           | The name of the interface inside the container
host\_name | string    | randomly assigned | no        | bridged, p2p, macvlan         | The name of the interface inside the host
hwaddr     | string    | randomly assigned | no        | all                           | The MAC address of the new interface
mtu        | integer   | parent MTU        | no        | all                           | The MTU of the new interface
parent     | string    | -                 | yes       | physical, bridged, macvlan    | The name of the host device or bridge

### Type: disk
Disk entries are essentially mountpoints inside the container. They can
either be a bind-mount of an existing file or directory on the host, or
if the source is a block device, a regular mount.

The following properties exist:

Key         | Type      | Default           | Required  | Description
:--         | :--       | :--               | :--       | :--
path        | string    | -                 | yes       | Path inside the container where the disk will be mounted
source      | string    | -                 | yes       | Path on the host, either to a file/directory or to a block device
optional    | boolean   | false             | no        | Controls whether to fail if the source doesn't exist
readonly    | boolean   | false             | no        | Controls whether to make the mount read-only
size        | string    | -                 | no        | Disk size in bytes (supports kB, MB, GB, TB, PB and EB suffixes). This is only supported for the rootfs (/).

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
Unix block device entries simply make the requested character device
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

## Profiles
Profiles can store any configuration that a container can (key/value or devices)
and any number of profiles can be applied to a container.

Profiles are applied in the order they are specified so the last profile
to specify a specific key wins.

In any case, resource-specific configuration always overrides that
coming from the profiles.


If not present, LXD will create a "default" profile which comes with a
network interface connected to LXD's default bridge (lxcbr0).

The "default" profile is set for any new container created which doesn't
specify a different profiles list.

## JSON representation
A representation of a container using all the different types of
configurations would look like:

    {
        'name': "my-container",
        'profiles': ["default"],
        'architecture': 'x86_64',
        'config': {
            'limits.cpu': '3',
            'security.privileged': 'true'
        },
        'devices': {
            'nic-lxcbr0': {
                'type': 'none'
            },
            'nic-mybr0': {
                'type': 'nic',
                'mtu': '9000',
                'parent': 'mybr0'
            },
            'rootfs': {
                'type': 'disk',
                'path': '/',
                'source': 'UUID=8f7fdf5e-dc60-4524-b9fe-634f82ac2fb6'
            },
        },
        'status': {
                    'status': "Running",
                    'status_code': 103,
                    'ips': [{'interface': "eth0",
                             'protocol': "INET6",
                             'address': "2001:470:b368:1020:1::2"},
                            {'interface': "eth0",
                             'protocol': "INET",
                             'address': "172.16.15.30"}]}
    }

