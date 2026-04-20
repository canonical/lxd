---
myst:
  html_meta:
    description: Release notes for LXD 6.8, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-6.8)=
# LXD 6.8 release notes

This is a {ref}`feature release <ref-releases-feature>` and is not recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
For a tour of [LXD UI](https://github.com/canonical/lxd-ui) updates, please see the release announcement in [our Discourse forum](https://discourse.ubuntu.com/t/lxd-6-8-released/80650).
```

(ref-release-notes-6.8-highlights)=
## Highlights

This section highlights new and improved features in this release.

### Cluster control-plane role

A new `control-plane` cluster member role has been added that can be manually assigned to designate which members participate in Raft consensus.

Control plane mode is inactive by default until at least 3 members are assigned the `control-plane` role.
While inactive, all cluster members remain eligible for automatic promotion to database roles (preserving existing behavior).
Once active, only `control-plane` members can become voters, standbys, or the database leader; members without the role are assigned `RAFT_SPARE` and excluded from automatic promotion.

When control plane mode is active, control-plane members also act as event hubs, replacing the now-deprecated `event-hub` role.

- Documentation: {ref}`cluster-manage-control-plane`
- API extension: {ref}`extension-clustering-control-plane`

### Cluster links

Cluster links enable secure, authenticated communication between separate LXD clusters using mutual TLS certificates.

This release adds a full cluster links API, including create/list/show/edit/rename/delete operations and state inspection support.
Matching `lxc cluster link ...` subcommands have also been added.

- Documentation: {ref}`exp-cluster-links`
- API extension: {ref}`extension-cluster-links`

### GPU CDI hotplug support for containers

Building on the AMD CDI container support added in LXD 6.7, GPU CDI devices can now be hotplugged into running containers.

- API extension: {ref}`extension-gpu-cdi-hotplug`

### Bulk instance state operations and metadata entity URL improvements

A new `recursion=2` mode for `GET /1.0/operations` returns the full parent-child relationship between operations.
`GET /1.0/operations/{id}` with `recursion=1` also now returns related child operations.

Parallel bulk instance state updates now create a parent operation with per-instance child operations, providing more granular status reporting.

Additionally operation metadata handling for `entity_url` has been tightened and expanded.
LXD now keeps the primary `entity_url` stable when metadata is updated and ensures it is present for applicable operations.

Rename operations can now expose both `entity_url` (the new target URL) and `original_entity_url` (the pre-rename URL), making rename tracking more reliable for API clients.

URL metadata coverage was also extended to additional create and rename operations, including project rename, instance rename/snapshot rename/backup rename, storage pool create, and storage volume create/snapshot rename/backup rename.

- API extension: {ref}`extension-bulk-operations`

### ZFS volume promotion support

A new {config:option}`storage-zfs-volume-conf:zfs.promote` configuration key has been added.
When set to `true`, this instructs LXD to ZFS-promote the volume when creating (or recreating) it from a clone.

This key is primarily useful when combined with `initial.*` {ref}`disk device configuration options <devices-disk-initial-config>` and allows controlling ZFS promotion when creating instances from other instances.

- API extension: {ref}`extension-storage-zfs-promote`

### Ceph RBD default features changed

New volumes (and clones) in Ceph RBD (`ceph`) pools are no longer created with only `--image-feature layering`.
Instead the default RBD features configured in the Ceph cluster are used.

If `ceph.rbd.features` is already set on a pool, that value continues to be used unchanged.

- API extension: {ref}`extension-storage-ceph-use-rbd-defaults`

### Ceph and CephFS support for messenger protocol v2

The Ceph storage driver now has support for the Ceph messenger protocol v2.

LXD now uses the native Ceph CLI tool (`ceph mon dump --format json`) instead of an internal `ceph.conf` parser for monitor discovery and FSID information. This enables Ceph messenger protocol v2 support.

These improvements enhance compatibility with modern Ceph deployments and provide more robust handling of various Ceph configurations, including those deployed through MicroCeph.

### Custom port numbers in NVMe and iSCSI storage connectors

The NVMe and iSCSI storage connectors now support custom port numbers, providing more flexibility when connecting to storage targets that do not use standard ports.

### OVN dynamic Northbound connection

When the {config:option}`server-miscellaneous:network.ovn.northbound_connection` server configuration is not set, LXD now dynamically determines the OVN Northbound database connection string based on the environment.
If the MicroOVN snap is used, LXD reads the configuration from the MicroOVN `ovn.env` file.
Otherwise, it defaults to `unix:/var/run/ovn/ovnnb_db.sock`.

This ensures that if the MicroOVN cluster membership changes, LXD will then use the updated OVN server connection configuration.

- API extension: {ref}`extension-ovn-dynamic-northbound-connection`

### Instance configuration refresh on copy

Instance `copy --refresh` operations now correctly apply target configuration, profile, and device updates server-side before the data transfer completes.
This applies to both direct copies and migration-based refresh operations.

- API extension: {ref}`extension-instance-refresh-config`

### Extended image metadata from SimpleStreams

Two new optional fields, `release_codename` and `release_title`, have been added to the `api.Image` struct.
These are populated from the SimpleStreams index when available.
The generated image description for SimpleStreams images now includes the variant when available, and no longer includes the creation date or architecture.

- API extension: {ref}`extension-image-extended-metadata`

### `lxc project get-current` command

A new `lxc project get-current` command has been added that outputs the name of the currently selected project, making it easy to use in scripts.

### `lxc --column`/`-c` flag for CSV output

The `--column`/`-c` flag is now supported by the `lxc` command everywhere that `--format csv` is accepted, allowing column selection to be combined with CSV output consistently across all `lxc` list commands.

### Stricter file permissions across the codebase

A large sweep of stricter file permissions has been applied across the codebase, reducing the risk of unintended access to sensitive files created by the LXD daemon and the `lxc` client.

### Widespread TOCTOU race condition fixes

Numerous time-of-check to time-of-use (TOCTOU) race conditions across the daemon, client, and storage drivers have been fixed, improving correctness and security under concurrent workloads.

### CSRF protection using Go standard library

The daemon now uses the CSRF protection provided by the Go standard library, replacing the previous custom implementation.

### Constant-time secret comparison

All secret comparison operations (exec, console, migration, and certificate token secrets) now use constant-time comparison to prevent timing side-channel attacks.

### HTTP hardening

Several HTTP hardening improvements have been applied to the daemon:

- Dropped the deprecated `X-XSS-Protection` response header.
- Added a `Referrer-Policy` header to prevent leaking referrer information.
- Applied HTTP timeouts to the pprof, Loki, and endpoint listeners.
- TCP keep alive and TCP user timeout configured on incoming API connections for faster stale connection detection.

(ref-release-notes-6.8-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`VM lowlevel restriction bypass via raw.apparmor and raw.qemu.conf (CVE-2026-34177)`](https://github.com/canonical/lxd/security/advisories/GHSA-fm2x-c5qw-4h6f)
- [{spellexception}`Update of type field in restricted TLS certificate allows privilege escalation to cluster admin (CVE-2026-34179)`](https://github.com/canonical/lxd/security/advisories/GHSA-c3h3-89qf-jqm5)
- [{spellexception}`Importing a crafted backup leads to project restriction bypass (CVE-2026-34178)`](https://github.com/canonical/lxd/security/advisories/GHSA-q96j-3fmm-7fv4)
- [{spellexception}`Arbitrary file read and write through pongo templates (CVE-2026-33897 from Incus)`](https://github.com/lxc/incus/security/advisories/GHSA-83xr-5xxr-mh92)
- [{spellexception}`Verify combined fingerprint when downloading images from simplestreams servers (CVE-2026-33542 from Incus)`](https://github.com/lxc/incus/security/advisories/GHSA-p8mm-23gg-jc9r)
- [{spellexception}`Fix creating instances using a local image from another project`](https://github.com/canonical/lxd/pull/17924)
- [{spellexception}`Require can_view on source instance and volume when copying`](https://github.com/canonical/lxd/pull/17914)
- [{spellexception}`Migration: Don't allow pull mode in restricted projects`](https://github.com/canonical/lxd/pull/17988)
- [{spellexception}`Use correct name in create-from-backup entity URL`](https://github.com/canonical/lxd/pull/17810)
- [{spellexception}`GPU CDI device fixes`](https://github.com/canonical/lxd/pull/17958)
- [{spellexception}`Fix snapshot URL in clustered mode`](https://github.com/canonical/lxd/pull/17794)
- [{spellexception}`Fix recursive file pull failing on existing directories and symlinks`](https://github.com/canonical/lxd/pull/17739)
- [{spellexception}`Fix --profile and --no-profiles flags being ignored on cluster moves`](https://github.com/canonical/lxd/pull/17756)
- [{spellexception}`Fix mutex leak and unclosed files`](https://github.com/canonical/lxd/pull/17778)
- [{spellexception}`Prevent concurrent evacuations`](https://github.com/canonical/lxd/pull/17475)
- [{spellexception}`Fix image fingerprint validation being too permissive`](https://github.com/canonical/lxd/pull/17985)
- [{spellexception}`Fix UI and documentation MIME type`](https://github.com/canonical/lxd/pull/18043)
- [{spellexception}`Enforce project limits.instances in clustered instance creation`](https://github.com/canonical/lxd/pull/17822)
- [{spellexception}`dnsmasq: clean up orphaned .removing files on bridge network start`](https://github.com/canonical/lxd/pull/17869)
- [{spellexception}`Improve phantom volume error reporting during cluster moves`](https://github.com/canonical/lxd/pull/18101)
- [{spellexception}`Fix instance copy to keep source architecture type`](https://github.com/canonical/lxd/pull/18102)
- [{spellexception}`Fix inverted TLS verification logic in Alletra client`](https://github.com/canonical/lxd/pull/18087)
- [{spellexception}`Mark images as cached consistently`](https://github.com/canonical/lxd/pull/16686)
- [{spellexception}`Fix deadlock by only taking storage pool and network creation lock for external API requests`](https://github.com/canonical/lxd/pull/18115)

(ref-release-notes-6.8-incompatible)=
## Backwards-incompatible changes

These changes are not compatible with older versions of LXD or its clients.

### MAAS controller support removed

The MAAS controller integration has been removed from LXD.
This removes all `maas.api.url`, `maas.api.key`, and `maas.machine` configuration keys, as well as the `maas.subnet.ipv4` and `maas.subnet.ipv6` NIC device options.

On upgrade, a patch automatically removes any MAAS-related configuration keys from the database.

### MinIO local object storage buckets removed

Local (non-Ceph) storage drivers no longer support object storage buckets.
Object storage buckets are now only supported by the `cephobject` driver.

The bundled `minio` binary and the `core.storage_buckets_address` configuration have been removed.
The `storage_buckets_local` API extension is no longer advertised.

### Ceph RBD and CephFS `source` configuration key dropped

The `source` configuration key for the `ceph` and `cephfs` storage drivers has been removed.
Use `ceph.osd.pool_name` for Ceph RBD pools and `cephfs.path` for CephFS pools instead.

On upgrade, a patch automatically unsets any stored `source` configuration keys for affected pools.

- API extension: {ref}`extension-storage-remote-drop-source`

### FAN bridge `fan.type=ipip` support removed

Support for `fan.type=ipip` in bridge networks has been removed.
Only `fan.type=vxlan` (the default) remains supported.

### Cluster role `event-hub` removed

The `event-hub` cluster role has been removed in favor of the new `control-plane` role, which provides equivalent event-hub behaviour alongside full Raft control-plane functionality.
Existing `event-hub` role assignments are automatically migrated to `control-plane` on upgrade.

- API extension: {ref}`extension-clustering-control-plane`

### Asynchronous storage pool, network, and storage bucket endpoints

Storage and network endpoints that were previously synchronous now return background operations.
This affects create, update, delete, and rename actions.

This includes storage pools, storage buckets (including bucket keys), networks, network ACLs, network zones, network zone records, network forwards, network load balancers and network peers.

Clients should check for this extension and handle the asynchronous response by waiting on the returned operation.
Operation metadata may include additional data, such as storage bucket admin credentials on bucket creation.

- API extension: {ref}`extension-storage-and-network-operations`

### Operation `resources` metadata no longer populated

Operation `resources` entries are now intentionally emptied and should no longer be relied upon by clients.

Historically, some clients used `resources` to infer the URL of entities created or affected by asynchronous operations.
With the 6.8 changes, clients should treat `entity_url` as authoritative for the operation target and, for rename operations, use `original_entity_url` (old URL) together with `entity_url` (new URL).

The `resources` field will be used in the future to record associated entities for an operation.

- API extension: {ref}`extension-bulk-operations`

### Public images restricted to the default project

Public images can no longer be created in non-default projects.
Attempts to mark images as public in non-default projects via image creation or update API endpoints will be rejected.

Images in non-default projects cannot be accessed by unauthenticated or unauthorized clients; only authenticated clients with appropriate permissions can view them.
To share images publicly, they must be created in or moved to the default project.

This change supports the forthcoming Image Registries feature.

### Migration pull mode into restricted projects no longer allowed

It is no longer possible to migrate instances and storage volumes into a restricted project when using `pull` migration mode.

### Go SDK changes

The following backwards-incompatible changes were made to the LXD Go SDK and will require updates to consuming applications.
These client functions are made to be backward compatible with older LXD servers.

- Storage pool `Create`, `Update`, and `Delete` functions now return an `Operation`.
- Storage bucket and bucket key `Create`, `Update`, and `Delete` functions now return an `Operation`.
- Network `Create`, `Update`, `Delete`, and `Rename` functions now return an `Operation`.
- Network ACL `Create`, `Update`, `Delete`, and `Rename` functions now return an `Operation`.
- Network peer `Create`, `Update`, and `Delete` functions now return an `Operation`.
- Network zone and network zone record `Create`, `Update`, and `Delete` functions now return an `Operation`.
- `GetInstances` variants unified into a single `GetInstances` method accepting an `args` struct.

(ref-release-notes-6.8-deprecated)=
## Deprecated features

These features are removed in this release.

### MAAS integration removed

All MAAS-related configuration options have been removed (see [Backwards-incompatible changes](#ref-release-notes-6.8-incompatible) above).

### Local MinIO storage buckets removed

Local object storage bucket support using MinIO has been removed (see [Backwards-incompatible changes](#ref-release-notes-6.8-incompatible) above).

## Known issues

### ARM64 VM Boot Failures (Synchronous Exception)

Booting virtual machines on ARM64 hardware (such as Raspberry Pi 4 and 5) may result in a Synchronous Exception early in the UEFI boot process.

Impact: Ubuntu 22.04 VMs fail to boot immediately. Ubuntu 24.04 VMs are also affected, with the failure threshold depending on the host's available memory.
Cause: This bug is tied to Secure Boot and is hypothesized to be related to memory availability and layout.

The workaround for now is disabling Secure Boot (setting {config:option}`instance-boot:boot.mode` to `uefi-nosecureboot`).

(ref-release-notes-6.8-go)=
## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.26.2.

(ref-release-notes-6.8-snap)=
## Snap packaging changes

- Minimum required `snapd` raised to `2.64`.
- QEMU bumped to `10.2.1+ds-1ubuntu3`.
- EDK2 rebased to `2025.11-3ubuntu7`.
- NVIDIA container toolkit updated to `1.19.0`.
- Go toolchain for snap builds bumped to `go1.26`.
- Removed MinIO-related snap config (`minio.path`) and MinIO support bits.
- Added the `ovn-env` plug for MicroOVN integration.
- Updated LXCFS handling to align with `pidfs` defaults and removed obsolete `lxcfs.pidfd` options/checks.
- Refactored ZFS setup through a dedicated `setup-zfs` helper script, including improved error handling and fallback behavior.

(ref-release-notes-6.8-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-6.7...lxd-6.8).

(ref-release-notes-6.8-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-6.8).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=6/stable`
- **MacOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
