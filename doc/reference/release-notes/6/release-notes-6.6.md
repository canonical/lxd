---
myst:
  html_meta:
    description: Release notes for LXD 6.6, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-6.6)=
# LXD 6.6 release notes

This is a {ref}`feature release <ref-releases-feature>` and is not recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
For a tour of [LXD UI](https://github.com/canonical/lxd-ui) updates, please see the release announcement in [our Discourse forum](https://discourse.ubuntu.com/t/lxd-6-6-has-been-released/72476).
```

(ref-release-notes-6.6-highlights)=
## Highlights

This section highlights new and improved features in this release.

### Instance placement groups

This release adds the concept of {ref}`placement groups <exp-clusters-placement>`.
Placement groups provide declarative control over how instances are distributed across cluster members.
They define both a **policy** (how instances should be distributed) and a **rigor** (how strictly the policy is enforced).
Placement groups are project-scoped resources, which means different projects can have placement groups with the same name without conflict.

- Documentation: {ref}`exp-clusters-placement`
- API extension: {ref}`extension-instance-placement-groups`

### Placement cluster member group recorded

When an instance is placed into a cluster member group using the `--target=@<group>` syntax, the group specified is now recorded into a new {config:option}`instance-volatile:volatile.cluster.group` configuration key.

This is then used during cluster member evacuation when {ref}`restoring instances <cluster-restore>` to ensure the instance placement remains within the specified group.

### Kubernetes Container Storage Interface (CSI) driver and `/dev/lxd` volume management

The LXD project now provides a CSI driver that allows Kubernetes to provision and manage volumes for K8s Pods.
The driver is an open source implementation of the Container Storage Interface (CSI) that integrates LXD storage backends with Kubernetes.
It leverages LXD's wide range of supported storage drivers, enabling dynamic provisioning of both local and remote volumes.
Depending on the storage pool, the CSI supports provisioning of both block and filesystem volumes.

To enable this functionality, the `/dev/lxd` guest API has been extended to support fine-grained authorization (by way of bearer token authentication) and volume management.

- Documentation: {ref}`exp-csi`
- Documentation: {ref}`devlxd-authenticate`
- API extension: {ref}`extension-auth-bearer-devlxd`
- API extension: {ref}`extension-devlxd-volume-management`

### Custom storage volume recovery improvements

Using the {ref}`extension-backup-metadataversion` improvements added in LXD 6.5, the {ref}`lxd recover <disaster-recovery>` tool now allows more extensive recovery of custom volumes attached to instances. The full custom volume configuration can now be recovered. Additionally, the tool now supports recovery from {ref}`storage-powerflex` and {ref}`storage-pure` pools which was previously not supported.

### Consistent instance and custom volume snapshots

Consistent snapshots of both an instance and its attached volumes can now be taken together.

The `lxc snapshot` command has been extended with the `--disk-volumes` flag that accepts either `root` or `all-exclusive` values.
When `root` is specified (the default behavior), a snapshot of just the instance's root volume is taken.
In `all-exclusive` mode, the instance is paused while a snapshot of its root volume and all exclusively attached volumes is taken.

An instance snapshot and its custom volume snapshots can be restored together using `lxc restore --disk-volumes=all-exclusive`.

- Documentation: {ref}`instances-snapshots`
- API extension: {ref}`extension-instance-snapshots-multi-volume`

### HPE Alletra storage driver

Initial support for using HPE Alletra storage appliances using iSCSI or NVME over TCP has been added.
Currently, instance and custom volume recovery is not supported (but it is planned).

- Documentation: {ref}`storage-alletra`
- API extension: {ref}`extension-storage-driver-alletra`

### Persistent VM PCIe bus allocation

Devices added to VMs now have their PCIe bus number persisted into volatile configuration keys so that the device maintains the same location on the bus when the instance is restarted. Previously, when a device was hot plugged into a running VM, it was possible for the operation to fail due to bus location conflicts or to succeed and then have its bus location change on a subsequent restart of the instance.

This change was also required to make the K8s CSI driver usable because it dynamically adds and removes custom filesystem volumes from running VMs.

- API extension: {ref}`extension-vm-persistent-bus`

### Per-project image and backup volumes

It has long been possible to specify that downloaded images and exported backups be stored in a custom volume on a particular storage pool.
It is now possible to specify these volumes on a per-project basis, allowing for images and backups to be stored in different custom volumes (and storage pools) for different projects.

Two new configuration keys have been introduced: {config:option}`server-miscellaneous:storage.project.{name}.images_volume` and {config:option}`server-miscellaneous:storage.project.{name}.backups_volume` per each project, allowing for a storage volume on an existing pool to be used for storing the project-specific images and backups artifacts.

- API extension: {ref}`extension-daemon-storage-per-project`

### OVN internal network forward and load balancers

This release adds support for internal OVN load balancers and network forwards.
This approach allows `ovn` networks to define ports on internal IP addresses that can be forwarded to other internal IPs inside their respective networks.
This change removes the previous limitation on `ovn` networks that load balancers and network forwards could only use external IP addresses to forward to internal IPs.

- API extension: {ref}`extension-ovn-internal-load-balancer`

### OVN DHCP ranges

This release adds a new configuration key {config:option}`network-ovn-network-conf:ipv4.dhcp.ranges` for `ovn` networks.
This key allows specifying a list of IPv4 ranges reserved for dynamic allocation using DHCP.
This is useful when setting up a {ref}`network forward <network-forwards>` towards a floating IP inside an `ovn` network that needs to be prevented from being allocated via DHCP.

- API extension: {ref}`extension-ovn-dhcp-ranges`

### OVN NIC acceleration parent interface option

This release adds support for specifying the OVN NIC acceleration physical function interfaces to allocate virtual functions from.

This avoids the need to add the physical function interfaces to the OVN integration bridge, which had prevented their use for host connectivity.

This change introduces a new configuration key for `ovn` networks and NICs:

- {config:option}`device-nic-ovn-device-conf:acceleration.parent` - Comma separated list of physical function (PF) interfaces from which to allocate virtual functions (VFs) from for hardware acceleration when {config:option}`device-nic-ovn-device-conf:acceleration` is enabled.

- API extension: {ref}`extension-ovn-nic-acceleration-parent`

### Improved OIDC authentication provider compatibility using sessions

This release adds session support for OIDC authentication. This enables compatibility with identity providers that issue opaque access tokens.

When a session expires, LXD re-verifies the login with the identity provider.
The duration of OIDC sessions defaults to one week and can be configured via the {config:option}`server-oidc:oidc.session.expiry` configuration key.

Verification of an OIDC session depends on a new, cluster-wide core secret.

A new {config:option}`server-core:core.auth_secret_expiry` configuration controls how long a secret remains valid before it expires.
This sets the upper bound of an OIDC session duration.

- API extension: {ref}`extension-auth-oidc-sessions`

### Create custom filesystem volume from tarball contents

The `lxc storage volume import` command has gained support for creating a custom filesystem volume from the contents of a tarball.

A new supported value of `tar` has been added to the `--type` flag that causes the contents of the tarball to be unpacked into the newly created volume.

- API extension: {ref}`extension-import-custom-volume-tar`

### Forced project deletion

It is now possible to forcefully delete a project and all of its entities using the `lxc project delete <project> --force` command.

- API extension: {ref}`extension-projects-force-delete`

### Operation requestor information

A new field `requestor` was added to operations, which contains information about the caller that initiated the operation.

- API extension: {ref}`extension-operation-requestor`

### Resources disk used by information

A new field `used_by` was added to disks in the resources API to indicate its potential use by any virtual parent device, such as `bcache`.

- API extension: {ref}`extension-resources-disk-used-by`

## UI updates

This release includes several improvements and new features in the LXD UI.

### SSH key generation during instance creation

The UI now supports generating SSH key pairs during instance creation, making it easier to configure instance access without relying on external tools.

### Bulk operations: View details

Bulk actions now include an expanded {guilabel}`View details` interface, allowing you to inspect aggregated information and per-item results when managing multiple resources at once.
For example, when performing bulk instance deletion or bulk instance start, the UI now shows which instances succeeded, which failed, and any associated messages for each item.

### Mobile experience improvements

Mobile-focused UI refinements improve navigation, responsiveness, and readability across smaller screens.

### Login project selection in settings

A new login project setting is available in the {guilabel}`Settings`.
The selected project is stored in `localStorage`, ensuring the UI restores your working context on return.

### HPE storage driver support

The UI now includes configuration and management support for the {guilabel}`HPE Alletra` storage driver, enabling pool and volume interaction for environments using this backend.

### Saved terminal connection defaults

Users can now save terminal connection defaults as an instance user key, allowing persistent preferences for how the terminal connects to instances.

### ACL support on instances and profiles

{guilabel}`ACLs` can now be added directly on {guilabel}`Instances` and {guilabel}`Profiles`, not just at the network level, enabling more granular access control configuration directly.

### MTU and VLAN support for physical networks

Physical network configuration forms now include {guilabel}`MTU` and {guilabel}`VLAN Id` fields, enabling more complete network definition from within the UI.

### Project configuration: restricted backups

The {guilabel}`Configuration` screen now exposes the {guilabel}`Instance` option to restrict backup creation on a project.

(ref-release-notes-6.6-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`Local privilege escalation through custom storage volumes (CVE-2025-64507)`](https://github.com/canonical/lxd/security/advisories/GHSA-3g2j-vm47-x4mj)
- [{spellexception}`Support for runc 1.3.3 inside containers`](https://github.com/canonical/lxd/issues/16902)
- [{spellexception}`Missing path encoding in non-recursive API responses`](https://github.com/canonical/lxd/issues/16792)
- [{spellexception}`S390x architecture name missing from architecture aliases`](https://github.com/canonical/lxd/issues/13497)
- [{spellexception}`lxc init doesn't immediately fail on duplicated instance name if the source image is not cached`](https://github.com/canonical/lxd/issues/12554)
- [{spellexception}`Restoring cluster member while evacuating breaks instance relationship with origin`](https://github.com/canonical/lxd/issues/15877)
- [{spellexception}`Cluster healing stops network on member that triggers healing`](https://github.com/canonical/lxd/issues/16642)
- [{spellexception}`NVIDIA CDI will not work with multiple GPUs when nvidia-persistenced is running`](https://github.com/canonical/lxd/issues/16227)
- [{spellexception}`Containers do not start again when the host is not shut down properly nvidia CDI`](https://github.com/canonical/lxd/issues/14843)
- [{spellexception}`Error parsing /proc/cpuinfo on Raspberry PI 5`](https://github.com/canonical/lxd/issues/16481)
- [{spellexception}`Listing instances through fine grained TLS auth is not reliable at scale`](https://github.com/canonical/lxd/issues/16614)
- [{spellexception}`Underlying storage uses 4096 bytes sector size when virtual machine images require 512 bytes`](https://github.com/canonical/lxd/issues/16477)
- [{spellexception}`Forcibly stopping an instance should not spam logs about leftover sftp server`](https://github.com/canonical/lxd/issues/15925)
- [{spellexception}`Concurrent (graphical) console connections to a VM don't close connections`](https://github.com/canonical/lxd/issues/16073)
- [{spellexception}`Help does not reflect, that there is a difference between lxc shell and lxc exec`](https://github.com/canonical/lxd/issues/16159)
- [{spellexception}`Network used by list incomplete`](https://github.com/canonical/lxd/issues/16216)
- [{spellexception}`The fanotify mechanism does not notice dynamic removal of underlying devices`](https://github.com/canonical/lxd/issues/15894)
- [{spellexception}`Removing a member from cluster group that is not in any other group silently ignores request`](https://github.com/canonical/lxd/issues/16074)
- [{spellexception}`Prune cached images during project delete`](https://github.com/canonical/lxd/pull/16623)

(ref-release-notes-6.6-incompatible)=
## Backwards-incompatible changes

These changes are not compatible with older versions of LXD or its clients.

### Asynchronous storage volume and profile API endpoints

Certain storage and profile endpoints that were previously synchronous now return an operation and behave asynchronously.

The latest LXD Go client detects the presence of this API extension. When it is available, the caller receives an operation object directly from the LXD server.
If the extension is not present on the server, then the server response is wrapped in a completed operation, allowing the caller to handle it as an operation while lacking a retrievable operation ID.

Older LXD Go clients are incompatible with servers that include this extension.
Instead of the expected successful response, they receive an operation response.

Endpoints converted to asynchronous behavior:

- `POST /storage-pools/{pool}/volumes/{type}` - Create storage volume
- `PUT /storage-pools/{pool}/volumes/{type}/{vol}` - Update storage volume
- `PATCH /storage-pools/{pool}/volumes/{type}/{vol}` - Patch storage volume
- `POST /storage-pools/{pool}/volumes/{type}/{vol}` - Rename storage volume
- `DELETE /storage-pools/{pool}/volumes/{type}/{vol}` - Delete storage volume
- `PUT /storage-pools/{pool}/volumes/{type}/{vol}/snapshots/{snap}` - Update storage volume snapshot
- `PATCH /storage-pools/{pool}/volumes/{type}/{vol}/snapshots/{snap}` - Patch storage volume snapshot
- `PUT /1.0/profiles/{name}` - Update profile
- `PATCH /1.0/profiles/{name}` - Patch profile

<!-- end list -->

- API extension: {ref}`extension-storage-and-profile-operations`

(ref-release-notes-6.6-deprecated)=
## Deprecated features

These features are removed in this release.

### Instance placement scriptlet removed

The instance placement scriptlet functionality (and the associated `instances_placement_scriptlet` API extension) has been removed in favor of the new {ref}`exp-clusters-placement` functionality.

If a scriptlet is set in the removed `instances.placement.scriptlet` configuration option, it is stored in the `user.instances.placement.script` configuration option when upgrading.

## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.25.4.

## Snap packaging changes

- Settings to disable the AppArmor restricted user namespaces are persisted to `/run/sysctl.d/zz-lxd.conf`
- Dqlite bumped to `v1.18.3`
- LXC bumped to `v6.0.5`
- LXCFS bumped to `v6.0.5`
- Enable `lxcfs.pidfd=true` by default
- LXD-UI bumped to `0.19`
- NVIDIA-container and toolkit bumped to `1.18.0`
- QEMU bumped to `8.2.2+ds-0ubuntu1.10`
- ZFS bumped to `zfs-2.3.4`

(ref-release-notes-6.6-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-6.5...lxd-6.6).

## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-6.6).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=6/stable`
- **MacOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
