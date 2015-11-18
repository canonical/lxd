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
core.trust\_password            | string        | -                         | Password to be provided by clients to setup a trust
storage.lvm\_vg\_name           | string        | -                         | LVM Volume Group name to be used for container and image storage. A default Thin Pool is created using 100% of the free space in the Volume Group, unless `storage.lvm_thinpool_name` is set.
storage.lvm\_thinpool\_name     | string        | "LXDPool"                 | LVM Thin Pool to use within the Volume Group specified in `storage.lvm_vg_name`, if the default pool parameters are undesirable.
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

Key                         | Type          | Default           | Description
:--                         | :---          | :------           | :----------
boot.autostart              | boolean       | false             | Always start the container when LXD starts
boot.autostart.delay        | int           | 0                 | Number of seconds to wait after the container started before starting the next one
boot.autostart.priority     | int           | 0                 | What order to start the containers in (starting with highest)
environment.\*              | string        | -                 | key/value environment variables to export to the container and set on exec
limits.cpus                 | int           | 0 (all)           | Number of CPUs to expose to the container
limits.memory               | int           | 0 (all)           | Size in bytes of the memory allocation for the container (supported suffixes: k, K, m, M, g or G)
raw.apparmor                | blob          | -                 | Apparmor profile entries to be appended to the generated profile
raw.lxc                     | blob          | -                 | Raw LXC configuration to be appended to the generated one
security.nesting            | boolean       | false             | Support running lxd (nested) inside the container
security.privileged         | boolean       | false             | Runs the container in privileged mode
user.\*                     | string        | -                 | Free form user key/value storage (can be used in search)
volatile.\<name\>.hwaddr    | string        | -                 | Unique MAC address for a given interface (generated and set by LXD when the hwaddr field of a "nic" type device isn't set)
volatile.base\_image        | string        | -                 | The hash of the image the container was created from, if any.
volatile.last\_state.idmap  | string        | -                 | Serialized container uid/gid map
volatile.last\_state.power  | string        | -                 | Container state as of last host shutdown


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
required for a standard POSIX system to work.
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

The currently supported device types and their properties are:
 - none (used to remove an inherited device) (dbtype = 0)
 - nic (network card) (dbtype = 1)
    - parent (name of the bridge or parent physical device on the host)
    - name (optional, if not specified, one will be assigned by the kernel)
    - hwaddr (optional, if not specified, one will be generated by LXD)
    - mtu (optional, if not specified, defaults to that of the parent)
    - nictype (optional, if not specified, defaults to "bridged")
 - disk (mounted storage) (dbtype = 2)
    - path (where to mount the disk in the container)
    - source (partition identifier or path on the host)
    - readonly (optional, whether to mount the disk read-only, defaults to false)
 - unix-char (UNIX character device) (dbtype = 3)
    - path (path relative to the container's root)
    - major (optional, if not specified, the same path on the host is mirrored)
    - minor (optional, if not specified, the same path on the host is mirrored)
    - uid (optional, if not specified, defaults t0 0)
    - gid (optional, if not specified, defaults to 0)
    - mode (optional, if not specified, defaults to 0660)
 - unix-block (UNIX block device) (dbtype = 4)
    - path (path relative to the container's root)
    - major (optional, if not specified, the same path on the host is mirrored)
    - minor (optional, if not specified, the same path on the host is mirrored)
    - uid (optional, if not specified, defaults to 0)
    - gid (optional, if not specified, defaults to 0)
    - mode (optional, if not specified, defaults to 0660)

Every device entry is identified by a unique name. If the same name is
used in a subsequent profile or in the container's own configuration,
the whole entry is overriden by the new definition.

Device entries are added through:
    lxc config device add <container> <name> <type> [key=value]...
    lxc profile device add <profile> <name> <type> [key=value]...

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
            'limits.cpus': '3',
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

