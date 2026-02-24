---
myst:
  html_meta:
    description: Release notes for LXD 6.7, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-6.7)=
# LXD 6.7 release notes

This is a {ref}`feature release <ref-releases-feature>` and is not recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
For a tour of [LXD UI](https://github.com/canonical/lxd-ui) updates, please see the release announcement in [our Discourse forum](https://discourse.ubuntu.com/t/lxd-6-6-has-been-released/72476).
```

(ref-release-notes-6.7-highlights)=
## Highlights

This section highlights new and improved features in this release.

### AMD GPU CDI support

LXD now supports AMD GPU passthrough for containers using the AMD CDI container-toolkit bundled in the snap package.

An AMD GPU device can be added to an instance using the command:

```
lxc config device add <instance_name> <device_name> gpu gputype=physical id=amd.com/gpu=0
```

- Documentation: {ref}`gpu-physical`
- API extension: {ref}`extension-gpu-cdi-amd`

### Improved VM GPU passthrough support with major new QEMU and EDK2 versions

As we approach the next LXD LTS release the snap package has been updated with QEMU 10.2 and EDK2 firmware 2025.02.
These represent significant version increases from the previous QEMU 8.2.2 and EDK2 2023.11.

In particular VM GPU device passthrough now offers increased compatibility due to dynamic MMIO window size support being enabled.

### Simplified initial access to the LXD UI

The `lxd init` command now offers the option to generate a UI temporary access link during initialization.

This temporary access URL can be used to directly access the LXD UI as an admin user for 24 hours, after which time the URL stops working.

The intention is that this temporary access can be used to quickly get started with LXD UI and allows for setting up permanent authentication methods such as {ref}`access-ui-setup-certificate` or {ref}`authentication-openid`.

- Documentation: {ref}`access-ui-setup-temporary-access-link`

### Storage pool database recovery support for clusters

As part of the database recovery process it might be necessary to scan existing storage pools previously created by LXD that still exist on the storage device.
Previously this was only possible for standalone LXD servers by using the `lxd recover` tool.

We have now re-worked the database disaster recovery process to support LXD clusters.
As part of this storage pools need to be re-created in the LXD database before running the `lxd recover` tool.
For storage pools that still exist on the storage device a new `source.recover` option is available that allows creating the storage pool database record without modifying the data on the storage device.

Previously this was only partially possible for some of the drivers (e.g. by using `lvm.vg.force_reuse`), but not directly supported.
The new pool `source.recover` configuration key can be set per cluster member to allow reuse of an existing pool `source`.

The `source.recover` option does not allow reusing the same source for multiple storage pools, however the LVM storage driver has the specific `lvm.vg.force_reuse` configuration key for this purpose.

- Documentation: {ref}`disaster-recovery`
- API extension: {ref}`extension-storage-source-recover`

### Forced instance deletion through API

This adds support for a `force` query parameter to the `DELETE /1.0/instances/{name}` endpoint. When set, running instances will be forcibly stopped before deletion.

This is now supported by the `lxc` CLI, rather than previously performing a force stop API call followed by a delete API call.

- API extension: {ref}`extension-instance-force-delete`

### Bearer authentication method

A new identity type `bearer` has been added that allows authentication with the LXD API using bearer tokens.

If applicable, the endpoint `/1.0/auth/identities/current` now also exposes the credential expiration time.
The `expires_at` field is set when the current identity is trusted and the authentication method is either `bearer` or `tls`.
In these cases, it reports the expiration time of the bearer token or the TLS certificate, respectively.

- Documentation: {ref}`authentication-bearer`
- API extension: {ref}`extension-auth-bearer-lxd`

### VM bus port limits

There is now a {config:option}`instance-resource-limits:limits.max_bus_ports` configuration key for virtual machines.
This option controls the maximum allowed number of user configurable devices that require a dedicated PCI/PCIe bus port.
This limit includes both the devices attached before the instance start and the devices hotplugged when the instance is running.
When the limit is set higher than the number of bus ports required at VM start time then the remainder of ports are usable for hot-plugging devices.

This limit was introduced to avoid the previous behaviour where 8 spare hot-plugging ports were added to VMs at start time.
This was non-deterministic as after hot-plugging up to the spare number of ports and then rebooting the VM a further 8 more spare ports would be added, which eventually could lead to the guest OS not being bootable.

This new setting allows control over how many bus ports are added to the VM.

- API extension: {ref}`extension-vm-limits-max-bus-ports`

### Optimized instance state field retrieval

Added support for selective recursion of state fields to speed up querying for instances in circumstances where not all state information is required.

The API now supports selective state field fetching using semicolon-separated syntax in the `recursion` parameter:

* `recursion=2;fields=state.disk` - Fetch only disk information
* `recursion=2;fields=state.network` - Fetch only network information
* `recursion=2;fields=state.disk,state.network` - Fetch both disk and network
* `recursion=2;fields=` - Fetch no expensive state fields (disk and network skipped)
* `recursion=2` - Fetch all fields (default behavior)

The `lxc list` command now automatically optimizes queries based on requested columns.

- API extension: {ref}`extension-instances-state-selective-recursion`

(ref-release-notes-6.7-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`Container environment configuration newline injection (CVE-2026-23953 from Incus)`](https://github.com/lxc/incus/security/advisories/GHSA-x6jc-phwx-hp32)
- [{spellexception}`Container image templating arbitrary host file read and write (CVE-2026-23954 from Incus)`](https://github.com/lxc/incus/security/advisories/GHSA-7f67-crqm-jgh7)
- [{spellexception}`Container hook project command injection (from Incus)`](https://github.com/lxc/incus/pull/2827/changes/0e0cf45ecdcc902a6f319f11971ed27df81bd29f)
- [{spellexception}`security.syscalls.intercept.mknod no longer for docker`](https://github.com/canonical/lxd/issues/14849)
- [{spellexception}`Instance POST changing target and project/pool cannot be mixed`](https://github.com/canonical/lxd/issues/15525)
- [{spellexception}`zfs.clone_copy=rebase option does not work for copying volumes`](https://github.com/canonical/lxd/issues/16449)
- [{spellexception}`TOCTOU error if images are downloaded concurrently`](https://github.com/canonical/lxd/issues/16687)
- [{spellexception}`Used by list of ACL shows instance multiple times if instance has multiple ACLs`](https://github.com/canonical/lxd/issues/17011)
- [{spellexception}`systemd services with credentials fail to start in containers with systemd v259 (Resolute)`](https://github.com/canonical/lxd/issues/17073)
- [{spellexception}`Volume snapshots can be attached using source=<vol>/<snap> rather than requiring use of source.snapshot key`](https://github.com/canonical/lxd/issues/17125)
- [{spellexception}`Volume snapshots disk devices are writable`](https://github.com/canonical/lxd/issues/17126)
- [{spellexception}`Unable to upgrade from 5.21 to 6.6: Assertion `header.wal_size == 0' failed`](https://github.com/canonical/lxd/issues/17174)
- [{spellexception}`Network create leaves stale database record if interrupted (context canceled)`](https://github.com/canonical/lxd/issues/17523)
- [{spellexception}`Instance logs are left behind after instance deletion`](https://github.com/canonical/lxd/issues/17618)
- [{spellexception}`dnsmasq log files are left behind after deleting the associated network`](https://github.com/canonical/lxd/issues/17619)

(ref-release-notes-6.7-incompatible)=
## Backwards-incompatible changes

These changes are not compatible with older versions of LXD or its clients.

### Minimum system requirement changes

The minimum supported version of some components has changed:

 - Kernel 6.8
 - LXC 5.0.0
 - QEMU 8.2.2
 - virt-v2v 2.3.4
 - ZFS 2.2

### VM `security.csm` and `security.secure_boot` options combined into `boot.mode` option

The `security.csm` and `security.secure_boot` VM options have been combined into the new {config:option}`instance-boot:boot.mode` configuration key to control the VM boot firmware mode.

The new setting accepts:
* `uefi-secureboot` (default) - Use UEFI firmware with secure boot enabled
* `uefi-nosecureboot` - Use UEFI firmware with secure boot disabled
* `bios` - Use legacy BIOS firmware (SeaBIOS), `x86_64` (`amd64`) only

- API extension: {ref}`extension-instance-boot-mode`

### Instance type specific API endpoints and Container specific Go SDK functions removed

The `/1.0/containers` and `/1.0/virtual-machines` endpoints have been removed along with all the container specific Go SDK functions.

Clients using these endpoints should be updated to use the combined `/1.0/instances` endpoints and `Instance` related Go SDK functions.

Documentation: {ref}`api-specification`

### Operation resources URL changes

Each {ref}`operation event <ref-events-operation>` has a `resources` field that contains URLs of LXD entities that the operation depends on.

When an instance, instance backup, or storage volume backup is created, it is not strictly required for the caller to provide the name of the new resource.
In this case, the URL of the expected resource was added to the resources map for clients to inspect and use.
The `resources` field then contains both a dependency of the operation, and the newly created resource (which may not exist yet).

To improve consistency, an optional `entity_url` field has been added to operation metadata that contains the URL of the entity that will be created.
The field is only included when a resource is being created asynchronously (operation response), and where it is not required for the entity name to be specified by the client.
For synchronous resource creation, clients should inspect the `Location` header for the same information.

The `resources` field will no longer contain this information.

Additionally the URLs presented in the `resources` field have been reviewed and in several cases updated to reflect the correct existing entities.

- API extension: {ref}`extension-operation-metadata-entity-name`

### Asynchronous project deletion

The {ref}`forced project deletion <extension-projects-force-delete>` API extension added support for forcibly deleting a project and all of its contents.
This can take a long time, but the `DELETE /1.0/projects/{name}` endpoint was previously returning a synchronous response.

Now this endpoint has been changed to an asynchronous operation response.
As with the {ref}`storage and profile operation extension <extension-storage-and-profile-operations>`, this extension is forward compatible only.

- API extension: {ref}`extension-project-delete-operation`

### Go SDK changes

The following backwards-incompatible changes were made to the LXD Go SDK and will require updates to consuming applications.
However these  client functions are made to be backward compatible with older LXD servers.

- [{spellexception}`DeleteInstance force argument`](https://github.com/canonical/lxd/commit/f4d9eb3d6f691afdbe6a4195804171a6e6945867)
- [{spellexception}`DeleteProject to return an Operation`](https://github.com/canonical/lxd/commit/c181ab91282d94e261f475fa993d776c75741c59)
- [{spellexception}`GetInstancesFull requires GetInstancesFullArgs and GetInstancesFullAllProjects, GetInstancesFullWithFilter and GetInstancesFullAllProjectsWithFilter removed`](https://github.com/canonical/lxd/commit/eedd2e4b456f3eaa4e43fd2f1ada3b50efb2ec06)

(ref-release-notes-6.7-deprecated)=
## Deprecated features

These features are removed in this release.

### VM 9p filesystem support for custom disk devices removed

Due to the change to QEMU 10.2 (which removed virtfs-proxy-helper support) LXD no longer supports exporting custom filesystem disk devices to VM guest using the 9p protocol. Custom filesystem disk devices can now only be exported to the VM guest using the virtiofs protocol.

However the read-only config drive used to bootstrap the lxd-agent inside the guest is still exported via both the 9p and virtiofs protocols for maximum lxd-agent guest OS compatibility.

## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.25.6.

## Snap packaging changes

- AMD container-toolkit added at `v1.2.0`
- EDK2 bumped to `2025.02-8ubuntu3`
- Dqlite bumped to `v1.18.5`
- LXD-UI bumped to `0.20`
- NVIDIA-container and toolkit bumped to `v1.18.2`
- QEMU bumped to `10.2.1+ds-1ubuntu1`
- ZFS bumped to `zfs-2.4.0`, `zfs-2.3.5`
- virtfs-proxy-helper removed (no longer supported by QEMU 10.2)

(ref-release-notes-6.7-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-6.6...lxd-6.7).

## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-6.7).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=6/stable`
- **MacOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
