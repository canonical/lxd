# API extensions

The changes below were introduced to the LXD API after the 1.0 API was finalized.

They are all backward compatible and can be detected by client tools by
looking at the api\_extensions field in GET /1.0/.


## storage\_zfs\_remove\_snapshots
A storage.zfs\_remove\_snapshots daemon configuration key was introduced.

It's a boolean that defaults to false and that when set to true instructs LXD
to remove any needed snapshot when attempting to restore another.

This is needed as ZFS will only let you restore the latest snapshot.

## container\_host\_shutdown\_timeout
A boot.host\_shutdown\_timeout container configuration key was introduced.

It's an integer which indicates how long LXD should wait for the container
to stop before killing it.

Its value is only used on clean LXD daemon shutdown. It defaults to 30s.

## container\_syscall\_filtering
A number of new syscalls related container configuration keys were introduced.

 * security.syscalls.blacklist\_default
 * security.syscalls.blacklist\_compat
 * security.syscalls.blacklist
 * security.syscalls.whitelist

See configuration.md for how to use them.

## auth\_pki
This indicates support for PKI authentication mode.

In this mode, the client and server both must use certificates issued by the same PKI.

See security.md for details.

## container\_last\_used\_at
A last\_used\_at field was added to the /1.0/containers/\<name\> GET endpoint.

It is a timestamp of the last time the container was started.

If a container has been created but not started yet, last\_used\_at field
will be 1970-01-01T00:00:00Z

## etag
Add support for the ETag header on all relevant endpoints.

This adds the following HTTP header on answers to GET:
 - ETag (SHA-256 of user modifiable content)

And adds support for the following HTTP header on PUT requests:
 - If-Match (ETag value retrieved through previous GET)

This makes it possible to GET a LXD object, modify it and PUT it without
risking to hit a race condition where LXD or another client modified the
object in the meantime.

## patch
Add support for the HTTP PATCH method.

PATCH allows for partial update of an object in place of PUT.

## usb\_devices
Add support for USB hotplug.

## https\_allowed\_credentials
To use LXD API with all Web Browsers (via SPAs) you must send credentials
(certificate) with each XHR (in order for this to happen, you should set
["withCredentials=true"](https://developer.mozilla.org/en-US/docs/Web/API/XMLHttpRequest/withCredentials)
flag to each XHR Request).

Some browsers like Firefox and Safari can't accept server response without
`Access-Control-Allow-Credentials: true` header. To ensure that the server will
return a response with that header, set `core.https_allowed_credentials=true`.

## image\_compression\_algorithm
This adds support for a compression\_algorithm property when creating an image (POST to /1.0/images).

Setting this property overrides the server default value (images.compression\_algorithm).

## directory\_manipulation
This allows for creating and listing directories via the LXD API, and exports
the file type via the X-LXD-type header, which can be either "file" or
"directory" right now.

## container\_cpu\_time
This adds support for retrieving cpu time for a running container.

## storage\_zfs\_use\_refquota
Introduces a new server property "storage.zfs\_use\_refquota" which instructs LXD
to set the "refquota" property instead of "quota" when setting a size limit
on a container. LXD will also then use "usedbydataset" in place of "used"
when being queried about disk utilization.

This effectively controls whether disk usage by snapshots should be
considered as part of the container's disk space usage.

## storage\_lvm\_mount\_options
Adds a new "storage.lvm\_mount\_options" daemon configuration option
which defaults to "discard" and allows the user to set addition mount
options for the filesystem used by the LVM LV.

## network
Network management API for LXD.

This includes:
 * Addition of the "managed" property on /1.0/networks entries
 * All the network configuration options (see configuration.md for details)
 * POST /1.0/networks (see rest-api.md for details)
 * PUT /1.0/networks/<entry> (see rest-api.md for details)
 * PATCH /1.0/networks/<entry> (see rest-api.md for details)
 * DELETE /1.0/networks/<entry> (see rest-api.md for details)
 * ipv4.address property on "nic" type devices (when nictype is "bridged")
 * ipv6.address property on "nic" type devices (when nictype is "bridged")
 * security.mac\_filtering property on "nic" type devices (when nictype is "bridged")

## profile\_usedby
Adds a new used\_by field to profile entries listing the containers that are using it.

## container\_push
When a container is created in push mode, the client serves as a proxy between
the source and target server. This is useful in cases where the target server
is behind a NAT or firewall and cannot directly communicate with the source
server and operate in pull mode.

## container\_exec\_recording
Introduces a new boolean "record-output", parameter to
/1.0/containers/<name>/exec which when set to "true" and combined with
with "wait-for-websocket" set to false, will record stdout and stderr to
disk and make them available through the logs interface.

The URL to the recorded output is included in the operation metadata
once the command is done running.

That output will expire similarly to other log files, typically after 48 hours.

## certificate\_update
Adds the following to the REST API:
 * ETag header on GET of a certificate
 * PUT of certificate entries
 * PATCH of certificate entries

## container\_exec\_signal\_handling
Adds support /1.0/containers/<name>/exec for forwarding signals sent to the
client to the processes executing in the container. Currently SIGTERM and
SIGHUP are forwarded. Further signals that can be forwarded might be added
later.

## gpu\_devices
Enables adding GPUs to a container.

## container\_image\_properties
Introduces a new "image" config key space. Read-only, includes the properties of the parent image.

## migration\_progress
Transfer progress is now exported as part of the operation, on both sending and receiving ends.
This shows up as a "fs\_progress" attribute in the operation metadata.

## id\_map
Enables setting the `security.idmap.isolated` and `security.idmap.isolated`,
`security.idmap.size`, and `raw.id_map` fields.

## network\_firewall\_filtering
Add two new keys, "ipv4.firewall" and "ipv6.firewall" which if set to
false will turn off the generation of iptables FORWARDING rules. NAT
rules will still be added so long as the matching "ipv4.nat" or
"ipv6.nat" key is set to true.

Rules necessary for dnsmasq to work (DHCP/DNS) will always be applied if
dnsmasq is enabled on the bridge.

## network\_routes
Introduces "ipv4.routes" and "ipv6.routes" which allow routing additional subnets to a LXD bridge.

## storage
Storage management API for LXD.

This includes:
* GET /1.0/storage-pools
* POST /1.0/storage-pools (see rest-api.md for details)

* GET /1.0/storage-pools/<name> (see rest-api.md for details)
* POST /1.0/storage-pools/<name> (see rest-api.md for details)
* PUT /1.0/storage-pools/<name> (see rest-api.md for details)
* PATCH /1.0/storage-pools/<name> (see rest-api.md for details)
* DELETE /1.0/storage-pools/<name> (see rest-api.md for details)

* GET /1.0/storage-pools/<name>/volumes (see rest-api.md for details)

* GET /1.0/storage-pools/<name>/volumes/<volume_type> (see rest-api.md for details)
* POST /1.0/storage-pools/<name>/volumes/<volume_type> (see rest-api.md for details)

* GET /1.0/storage-pools/<pool>/volumes/<volume_type>/<name> (see rest-api.md for details)
* POST /1.0/storage-pools/<pool>/volumes/<volume_type>/<name> (see rest-api.md for details)
* PUT /1.0/storage-pools/<pool>/volumes/<volume_type>/<name> (see rest-api.md for details)
* PATCH /1.0/storage-pools/<pool>/volumes/<volume_type>/<name> (see rest-api.md for details)
* DELETE /1.0/storage-pools/<pool>/volumes/<volume_type>/<name> (see rest-api.md for details)

- All storage configuration options (see configuration.md for details)

## file\_delete
Implements DELETE in /1.0/containers/\<name\>/files

## file\_append
Implements the X-LXD-write header which can be one of "overwrite" or "append".

## network\_dhcp\_expiry
Introduces "ipv4.dhcp.expiry" and "ipv6.dhcp.expiry" allowing to set the DHCP lease expiry time.

## storage\_lvm\_vg\_rename
Introduces the ability to rename a volume group by setting "storage.lvm.vg\_name".

## storage\_lvm\_thinpool\_rename
Introduces the ability to rename a thinpool name by setting "storage.thinpool\_name".

## network\_vlan
This adds a new "vlan" property to "macvlan" network devices.

When set, this will instruct LXD to attach to the specified VLAN. LXD
will look for an existing interface for that VLAN on the host. If one
can't be found it will create one itself and then use that as the
macvlan parent.

## image\_create\_aliases
Adds a new "aliases" field to POST /1.0/images allowing for aliases to
be set at image creation/import time.

## container\_stateless\_copy
This introduces a new "live" attribute in POST /1.0/containers/NAME.
Setting it to false tells LXD not to attempt running state transfer.

## container\_only\_migration
Introduces a new boolean "container\_only" attribute. When set to true only the
container will be copied or moved.

## storage\_zfs\_clone\_copy
Introduces a new boolean "storage\_zfs\_clone\_copy" property for ZFS storage
pools. When set to false copying a container will be done through zfs send and
receive. This will make the target container independent of its source
container thus avoiding the need to keep dependent snapshots in the ZFS pool
around. However, this also entails less efficient storage usage for the
affected pool.
The default value for this property is true, i.e. space-efficient snapshots
will be used unless explicitly set to "false".

## unix\_device\_rename
Introduces the ability to rename the unix-block/unix-char device inside container by setting "path",
and the "source" attribute is added to specify the device on host.
If "source" is set without a "path", we should assume that "path" will be the same as "source".
If "path" is set without "source" and "major/minor" isn't set,
we should assume that "source" will be the same as "path".
So at least one of them must be set.

## storage\_rsync\_bwlimit
When rsync has to be invoked to transfer storage entities setting rsync.bwlimit
places an upper limit on the amount of socket I/O allowed.

## network\_vxlan\_interface
This introduces a new tunnel.NAME.interface option for networks.

This key control what host network interface is used for a VXLAN tunnel.

## storage\_btrfs\_mount\_options
This introduces the btrfs.mount\_options property for btrfs storage pools.

This key controls what mount options will be used for the btrfs storage pool.

## entity\_description
This adds descriptions to entities like containers, snapshots, networks, storage pools and volumes.

## image\_force\_refresh
This allows forcing a refresh for an existing image.

## storage\_lvm\_lv\_resizing
This introduces the ability to resize logical volumes by setting the "size"
property in the containers root disk device.

## id\_map\_base
This introduces a new security.idmap.base allowing the user to skip the
map auto-selection process for isolated containers and specify what host
uid/gid to use as the base.

## file\_symlinks
This adds support for transfering symlinks through the file API.
X-LXD-type can now be "symlink" with the request content being the target path.

## container\_push\_target
This adds the "target" field to POST /1.0/containers/NAME which can be
used to have the source LXD host connect to the target during migration.

## network\_vlan\_physical
Allows use of "vlan" property with "physical" network devices.

When set, this will instruct LXD to attach to the specified VLAN on the "parent" interface.
LXD will look for an existing interface for that "parent" and VLAN on the host.
If one can't be found it will create one itself.
Then, LXD will directly attach this interface to the container.

## storage\_images\_delete
This enabled the storage API to delete storage volumes for images from
a specific storage pool.

## container\_edit\_metadata
This adds support for editing a container metadata.yaml and related templates
via API, by accessing urls under /1.0/containers/NAME/metadata. It can be used
to edit a container before publishing an image from it.

## container\_snapshot\_stateful\_migration
This enables migrating stateful container snapshots to new containers.

## storage\_driver\_ceph
This adds a ceph storage driver.

## storage\_ceph\_user\_name
This adds the ability to specify the ceph user.
