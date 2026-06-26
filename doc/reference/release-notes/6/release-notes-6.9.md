---
myst:
  html_meta:
    description: Release notes for LXD 6.9, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-6.9)=
# LXD 6.9 release notes

This is a {ref}`feature release <ref-releases-feature>` and is not recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
For a tour of [LXD UI](https://github.com/canonical/lxd-ui) updates, please see the release announcement in [our Discourse forum](https://discourse.ubuntu.com/t/lxd-6-9-has-been-released/84211).
```

(ref-release-notes-6.9-highlights)=
## Highlights

This section highlights new and improved features in this release.

### Network load balancer pools

Load balancer pools have been introduced for OVN networks, allowing instances to be grouped together as targets for load balancer traffic distribution.

Pools provide a way to manage collections of backend instances that receive forwarded traffic from load balancers, with health checking support and dynamic membership based on instance availability.

New `lxc network load-balancer pool` subcommands have been added to manage these pools.

- Documentation: {ref}`network-load-balancers`
- API extension: {ref}`extension-network-load-balancer-pool`

### PowerStore storage driver

A new `powerstore` storage driver has been added, enabling the use of Dell PowerStore storage arrays with LXD.

The driver supports iSCSI and Fibre Channel (FC) connectivity modes, providing flexible options for connecting to PowerStore volumes.

- Documentation: {ref}`storage-powerstore`
- API extension: {ref}`extension-storage-driver-powerstore`

### Fibre Channel storage connector

A new Fibre Channel (FC) storage connector has been added, enabling FC-based connectivity for remote storage drivers.

This allows supporting storage drivers like PowerStore to use FC transport for volume attachment, in addition to the existing iSCSI and NVMe/TCP options.

### PowerFlex 5 support

The PowerFlex storage driver has been updated to support Dell PowerFlex version 5, including thin clone support and updated API compatibility.

The driver now automatically detects the PowerFlex version and adapts its behavior accordingly, maintaining backwards compatibility with PowerFlex 4.

### Security events

A new `security` event type has been added, providing OWASP-compliant audit logging for security-relevant operations.

Security events cover authentication (login failures, token operations, certificate changes), authorization (permission denials, admin actions), and system events (startup, shutdown, monitoring changes).

These events can be routed to Grafana Loki by adding `security` to the `loki.types` server configuration.

- Documentation: {ref}`howto-security-events`
- API extension: {ref}`extension-event-security`

### OIDC device client ID

A new {config:option}`server-oidc:oidc.device.client.id` configuration key has been added to support separate OAuth clients for CLI authentication.

This allows administrators to configure a dedicated device authorization grant client for the `lxc` CLI, separate from the main OIDC client used by the LXD UI, enabling different authentication flows and security requirements for each.

- API extension: {ref}`extension-oidc-device-client-id`

### Optimized ZFS instance creation with image variants

The ZFS storage driver now supports optimized instance creation through image variants.

When creating instances, LXD can now cache and reuse ZFS clones that match the instance's configuration (such as initial block mode or filesystem type), significantly improving creation time for subsequent instances using the same image variant.

Stale image variants are automatically cleaned up when pool configuration changes.

- Documentation: {ref}`storage-zfs-internals`

### Project replica mode

Projects now have an explicit `replica_mode` field that indicates whether a project is in `leader` or `standby` mode for replication purposes.

New `lxc project promote-replica` and `lxc project demote-replica` commands have been added to manage project replication state, and instances in standby projects are prevented from starting.

- API extension: {ref}`extension-project-replica-mode`

### Cluster links `used_by` field

Cluster links now include a `used_by` field that lists all entities referencing the link, such as replicators.

This enables better visibility into cluster link dependencies and prevents accidental deletion or renaming of in-use links.

- API extension: {ref}`extension-cluster-links-used-by`

### Switch to Go standard library HTTP router

The LXD daemon has switched from `gorilla/mux` to the Go standard library's `http.ServeMux` for HTTP routing.

This reduces external dependencies and leverages the improved routing capabilities added in Go 1.22.

### OIDC verifier background initialization

The OIDC verifier is now initialized in the background on daemon startup, with automatic retry on failure.

This prevents LXD startup from being blocked by unavailable identity providers and adds a warning when OIDC authentication is unavailable.

### Cluster evacuation quorum protection and raft rebalancing

Cluster member evacuation now validates the raft quorum before proceeding and refuses to evacuate an online voter when doing so would drop the remaining online voters below the required majority.

During evacuation, LXD transfers leadership first when needed, marks the member as evacuated, and triggers an immediate raft rebalance so that evacuated members are demoted and excluded from promotion candidacy before workloads are migrated. Restore reverses this by prioritizing networks and instances before triggering a raft rebalance.

The quorum safety check can be bypassed by passing `force` to the evacuate action.

- API extension: {ref}`extension-clustering-evacuation-force`

## UI updates

This release introduces replicator management, load balancer support, and an initial file explorer for instances, alongside cluster link enhancements, identity improvements, and a range of user-driven refinements.

### Replicators

- Added full replicator management, including:
    - Project configuration page to set up replicators and cluster links
    - Replicator list page
    - Detailed replicator view
    - Create and edit workflows
    - Run modal for manual execution
    - Rich status chips and visual indicators
    - Instance usage visibility per project
- Improved replicator validation, permissions handling, and overall user experience.

### Load balancers

- Added load balancer management for OVN networks to the UI.
- Added support for managing load balancer instances.

### Storage

- Improved Dell PowerStore support with updates to PowerStore configuration and management workflows.
- Improved storage-related form behavior and validation.

### Instance experience

- Introduced the initial Instance File Explorer implementation.
- Added support for file and directory deletion within the File Explorer.
- Improved instance creation workflow by automatically focusing newly added profiles.
- Fixed image tab state handling in the All Projects view.

### Identity and access management

- OIDC configuration can now be accessed directly from Settings when managing identities and permissions.
- Improved identity page header controls and alignment.

### Cluster management

- Enhanced cluster links with:
    - Rich chips for improved visibility
    - Better creation and confirmation workflows
    - Improved copy and guidance throughout the UI
    - Protection against deleting cluster links that are currently in use
- Cluster links are now displayed on single-node servers where applicable.
- Improved handling of cluster link tokens and redirects.
- The "Same for all members" option is now hidden when a cluster contains only a single member.

### User-driven improvements

- Improved cluster link onboarding and setup guidance.
- Standardized navigation and redirection behavior across cluster-linked environments.
- Replaced Monaco Editor with CodeMirror for YAML and configuration editing.

### Bug fixes

- Fixed issues with image registry renaming when rename permissions are unavailable.
- Fixed sorting of modified timestamps.
- Fixed several cluster link validation, token handling, and UX issues.

(ref-release-notes-6.9-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`Project restriction bypass in instance copy across projects (CVE-2026-55622)`](https://github.com/canonical/lxd/security/advisories/GHSA-qx75-2p3r-pwm5)
- [{spellexception}`Project restriction bypass for custom volume copy across projects (CVE-2026-55621)`](https://github.com/canonical/lxd/security/advisories/GHSA-7mr3-28h5-m5vx)
- [{spellexception}`Restricted project bypass leading to arbitrary command execution (CVE-2026-48751)`](https://github.com/canonical/lxd/security/advisories/GHSA-47w9-6r3f-938g)
- [{spellexception}`Arbitrary file write on host via `exec-output` symlink in crafted image (CVE-2026-48750)`](https://github.com/canonical/lxd/security/advisories/GHSA-9j25-mm2h-2f76)
- [{spellexception}`Arbitrary file read+write on host via templates/ symlink in malicious image (CVE-2026-48752)`](https://github.com/canonical/lxd/security/advisories/GHSA-jpf8-86f3-wp38)
- [{spellexception}`Arbitrary file read+write on host via rootfs/ symlink in malicious image (CVE-2026-48749)`](https://github.com/canonical/lxd/security/advisories/GHSA-vghh-5rfx-xhq8)
- [{spellexception}`Argument injection in backup compression algorithm leading to AFW and ACE (CVE-2026-48755)`](https://github.com/canonical/lxd/security/advisories/GHSA-fmc8-p6q7-75cc)
- [{spellexception}`Arbitrary file write on client due to trusted image hash (CVE-2026-48769)`](https://github.com/canonical/lxd/security/advisories/GHSA-pjff-c2wc-f6jm)
- [{spellexception}`Cross-guest volume hijack via DevLXD device patch (CVE-2026-12411)`](https://github.com/canonical/lxd/security/advisories/GHSA-hhf9-qw4v-72xp)
- [{spellexception}`CreateCustomVolumeFromBackup nil-pointer dereference on volumes[0].snapshots[*].expires_at (CVE-2026-9639)`](https://github.com/canonical/lxd/security/advisories/GHSA-j93m-3j9p-m5m8)
- [{spellexception}`Backup snapshot import bypasses project restrictions (CVE-2026-9640)`](https://github.com/canonical/lxd/security/advisories/GHSA-ppq7-4492-5552)
- [{spellexception}`Fix replicator failing to process instances on remote cluster members`](https://github.com/canonical/lxd/pull/18207)
- [{spellexception}`Fix replicator backup file write errors on idempotent refresh operations`](https://github.com/canonical/lxd/pull/18207)
- [{spellexception}`Prevent duplicate replicators targeting the same cluster link`](https://github.com/canonical/lxd/pull/18442)
- [{spellexception}`Fix replicator restore for unclustered standby clusters`](https://github.com/canonical/lxd/pull/18312)
- [{spellexception}`Fix ZFS storage pool leak after shutdown due to lingering forkfile daemon`](https://github.com/canonical/lxd/pull/18446)
- [{spellexception}`Fix listing images with --all-projects flag`](https://github.com/canonical/lxd/pull/18371)
- [{spellexception}`Fix LVM thin-pool usage calculation accuracy`](https://github.com/canonical/lxd/pull/18311)
- [{spellexception}`Fix btrfs send failure during refresh migration`](https://github.com/canonical/lxd/pull/18207)
- [{spellexception}`Fix instance copy refresh when target does not exist`](https://github.com/canonical/lxd/pull/18207)
- [{spellexception}`Fix race condition in Rename with pending forkfile cleanup`](https://github.com/canonical/lxd/pull/18254)
- [{spellexception}`Fix events websocket disconnection logic in the client`](https://github.com/canonical/lxd/pull/18365)
- [{spellexception}`Fix hung goroutine on ReadJSON in events`](https://github.com/canonical/lxd/pull/18359)
- [{spellexception}`Address possible iSCSI race conditions`](https://github.com/canonical/lxd/pull/18332)
- [{spellexception}`Fix missing secure boot firmware message for VMs`](https://github.com/canonical/lxd/pull/18375)
- [{spellexception}`Improve snapshot config validation during import`](https://github.com/canonical/lxd/pull/18301)
- [{spellexception}`Fix backup file error handling for refresh`](https://github.com/canonical/lxd/pull/18242)
- [{spellexception}`Fix local config being wiped out by reverter`](https://github.com/canonical/lxd/pull/18180)
- [{spellexception}`Fix operation entity URLs for storage volumes/backups/snapshots`](https://github.com/canonical/lxd/pull/18167)
- [{spellexception}`Use merged node config for local pool creation`](https://github.com/canonical/lxd/pull/18270)
- [{spellexception}`Bypass HTTP proxy for cluster connections`](https://github.com/canonical/lxd/pull/18338)
- [{spellexception}`Bypass proxy when retrieving certificate during cluster join`](https://github.com/canonical/lxd/pull/18354)
- [{spellexception}`Include slab_reclaimable in MemAvailable metric`](https://github.com/canonical/lxd/pull/18289)
- [{spellexception}`Fix architecture filter to match displayed values in lxc image`](https://github.com/canonical/lxd/pull/18292)
- [{spellexception}`Validate snapshot.ExpiresAt is non-nil`](https://github.com/canonical/lxd/pull/18320)
- [{spellexception}`Use legacy CephFS mount syntax on kernel < 5.17`](https://github.com/canonical/lxd/pull/18192)
- [{spellexception}`Fix Identity API group management visibility`](https://github.com/canonical/lxd/pull/18177)
- [{spellexception}`Allow dynamic OVN NIC address updates`](https://github.com/canonical/lxd/pull/18156)
- [{spellexception}`Mount Ceph RBD snapshots read-only to support modern ext4`](https://github.com/canonical/lxd/pull/18469)
- [{spellexception}`Work with modern LVM`](https://github.com/canonical/lxd/pull/18463)
- [{spellexception}`Add missing content types for storage volume POST`](https://github.com/canonical/lxd/pull/18457)

(ref-release-notes-6.9-incompatible)=
## Backwards-incompatible changes

These changes are not compatible with older versions of LXD or its clients.

### NVMe/TCP storage pool mode renamed

The storage pool NVMe/TCP mode has been renamed from `nvme` to `nvme/tcp` for clarity and consistency with other transport modes.

Existing pools using the `nvme` mode are automatically migrated to use `nvme/tcp` on upgrade.

- API extension: {ref}`extension-storage-nvme-tcp`

### Image import from client-specified URL removed

Support for importing images from a client-specified URL (the `direct` protocol) has been removed.

Images should be imported using the standard image server protocols (simplestreams or LXD).
Existing images using this deprecated source type will no longer auto-update.

### `lxc cluster evacuate` and `restore` flag changes

The `--force` flag of `lxc cluster evacuate` and `lxc cluster restore` no longer acts as a confirmation bypass. It now bypasses the server-side raft quorum safety check instead.

Use the new `--yes` flag to skip the interactive confirmation prompt, matching the convention used elsewhere in the `lxc` CLI.

- API extension: {ref}`extension-clustering-evacuation-force`

(ref-release-notes-6.9-known-issues)=
## Known issues

This section covers known temporary limitations and integration regressions in this release.

### CDI GPU passthrough failure on Ubuntu Core 26

Users attempting to pass through GPUs to containers on Ubuntu Core 26 environments using the `gpu-2604` interface (provided by the `mesa-2604` snap) will encounter a container startup failure:

```
Error: Failed starting device "gpu0": Failed generating CDI spec: Failed determining NVIDIA driver root path: Failed running: /snap/lxd/<revision>/gpu-2604/bin/gpu-2604-provider-wrapper printenv NVIDIA_DRIVER_ROOT: exit status 1
```

This is caused by an upstream architectural mismatch on the Core 26 track between the `pc-kernel` snap and the `mesa-2604` graphics provider snap.
The `pc-kernel` snap exposes NVIDIA driver files using new interfaces, but the `mesa-2604` wrapper script is still looking for legacy `kernel-gpu-2604` directory paths.

There is currently no native LXD configuration workaround.

### Strict process tracking failures with `core26` snap base

For applications running on `core26` and newer bases, `snapd` enforces stricter process tracking and device cgroup validations that rely on an active `systemd` user session.

If you run `lxc` commands inside environments where a user identity was switched without spawning a corresponding user session (for example, non-interactive scripts or mechanisms like `sudo`, `su`, or `runuser`), the execution will fail with the following error:

```
The user ubuntu cannot run snap applications on this system.
See https://forum.snapcraft.io/t/46210 for more details.
internal error, please report: running "lxd.lxc" failed: cannot track application process
```

This behavior is triggered by a bug in `snapd` where the enforcement logic incorrectly checks for an application-specific security tag instead of the overarching snap security tag when validating self-managed device cgroups support. As a result, `snapd` fails to fall back safely when a standard `systemd` user session is absent. A fix is on its way in a [future `snapd` release](https://github.com/canonical/snapd/pull/17238), but until then, users can work around this issue by ensuring that a user session is active when running LXD commands.

Possible workarounds include:

1. Ensure a user session is active when running `lxc` commands.
2. `loginctl enable-linger USERNAME` to enable a persistent user session for the specified user.

(ref-release-notes-6.9-go)=
## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.26.4.

(ref-release-notes-6.9-snap)=
## Snap packaging changes

- Transitioned the snap base from `core24` to `core26`.
- LXCFS: Reverted partial backport of PSI functionality that prevented host machine suspend ([#17983](https://github.com/canonical/lxd/issues/17983)).
- libnvidia-container bumped to v1.19.1.
- AMD ROCm container toolkit bumped to v1.3.0.
- ZFS 2.2 bumped to 2.2.10.
- ZFS 2.3 bumped to 2.3.8.
- ZFS 2.4 bumped to 2.4.3.
- Removed unused `arptables` binary.
- Removed `libcephfs` from the snap due to unusable missing dependencies.
- Removed unneeded Python dependencies from Ceph.
- Various snap size optimizations (removed unused QEMU keymaps, LXD UI localization files, uefivars bloat).

(ref-release-notes-6.9-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-6.8...lxd-6.9).

(ref-release-notes-6.9-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-6.9).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=6/stable`
- **MacOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
