---
myst:
  html_meta:
    description: Release notes for LXD 6.10.0, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-6.10.0)=
# LXD 6.10.0 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

This is the first LTS release for the 6 series.
It consolidates new features, storage and networking advancements, disaster recovery capabilities, security hardening, and bug fixes from across the 6 series.

```{note} Release notes content
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
For a tour of [LXD UI](https://github.com/canonical/lxd-ui) updates, please see the release announcement in [our Discourse forum](https://discourse.ubuntu.com/t/lxd-6-10-0-lts-has-been-released/89062).
```

(ref-release-notes-6.10.0-highlights)=
## Highlights

This section highlights new and improved features in this release.

### Image registries

LXD now supports first-class image registries. An image registry is a read-only source of images. 
An administrator can use image registries to control where tenants are able to download images for LXD.

Projects can restrict image downloads to specific authorized image registries using the new `restricted.registries` project configuration key.

Image registries support using another LXD cluster as an image source by referencing a {ref}`cluster link <exp-cluster-links>`.
Simple Streams image servers are also supported.

Sending the URL of a remote image server in the contents of an image source remains supported, but is deprecated functionality.
A comprehensive compatibility layer was added to support older LXD clients with the transition.

- Documentation: {ref}`ref-image-registries` and {ref}`howto-image-registries`
- API extension: {ref}`extension-image-registries`

### Persistent changed block tracking for VM block volumes

```{warning}
Changed block tracking is a feature preview and **must not** be enabled in a production environment.
See {ref}`howto-snap-configure-feature-previews` for more details.
```

Virtual machine block volumes now support persistent changed block tracking (CBT).

LXD leverages QEMU dirty bitmaps and NBD export interfaces to track disk blocks modified between snapshots.
External backup tools and migration pipelines can query and download only the altered blocks rather than whole disk images, significantly reducing backup windows, bandwidth usage, and storage overhead.

- Documentation: {ref}`howto-storage-block-tracking`
- API extension: {ref}`extension-storage-volume-block-tracking`

### Ceph RBD mirroring for project volume replication

Disaster recovery replicators now support asynchronous Ceph RBD storage mirroring.

When replicating a project's instances and exclusively attached volumes using Ceph RBD storage pools, LXD coordinates with Ceph's native volume mirroring capabilities instead of performing userspace block transfers.
Standby clusters receive mirrored image updates natively, and LXD manages all volume promotion, demotion, and leader failover operations.

- Documentation: {ref}`exp-replicators`
- API extension: {ref}`extension-storage-ceph-replicator`

### Custom volume support in replicators

Disaster recovery replication now supports custom storage volumes with the introduction of the `all-exclusive` disk volumes mode for instance migration.

When migrating or replicating instances, any custom storage volumes attached exclusively to the instance, along with their volume snapshots, are automatically transferred in the same operation.
Volumes attached to multiple instances or shared across the project are not replicated.

- Documentation: {ref}`exp-replicators`
- API extension: {ref}`extension-replicator-custom-volumes`

### Disaster recovery and replicator metrics

Dedicated Prometheus metrics and recovery point objective (RPO) tracking have been added for disaster recovery replicators.

LXD now exposes gauges for `lxd_replicators`, `lxd_replicator_last_run_status`, `lxd_replicator_last_success_timestamp`, and `lxd_replicator_last_success_oldest_snapshot_timestamp`.
These metrics provide real-time visibility into replication health and the age of the oldest replicated snapshot.

- Documentation: {ref}`provided-metrics`
- API extension: {ref}`extension-metrics-replicators`

### Unidirectional cluster links

The cluster links API now supports unidirectional relationships.

A local cluster can establish an authenticated link to a remote cluster by consuming an authorization trust token issued by the remote cluster.
The remote cluster validates and authenticates incoming requests from the local cluster while preventing outbound connections back to the local cluster, accommodating network topologies with strict firewall or one-way ingress rules.

- Documentation: {ref}`exp-cluster-links`
- API extension: {ref}`extension-cluster-links-unidirectional`

### Public cluster links

Cluster links can now connect to public or read-only remote clusters without client certificate authentication.

Public cluster links verify the remote endpoint using TLS certificate fingerprint pinning during a two-phase creation workflow.
This enables access to public services and image registries without creating reciprocal identities or exchanging mutual credentials.

- Documentation: {ref}`exp-cluster-links`
- API extension: {ref}`extension-cluster-links-public`

### OVN load balancer pool health checks

Health checking support has been introduced for OVN load balancer backend pools.

Administrators can configure active health checks using the {config:option}`network-load-balancer-pool-properties:healthcheck` settings on the pool, specifying interval, timeout, and consecutive success/failure thresholds.
The health state of backend instances can be inspected via the new load balancer pool state API.

- Documentation: {ref}`network-load-balancers`
- API extension: {ref}`extension-network-load-balancer-pool-health-checks`

### Pure Storage FlashArray Fibre Channel support

The Pure Storage FlashArray storage driver has expanded enterprise SAN connectivity options by adding Fibre Channel transport support.

Storage pools can now be configured using SCSI over Fibre Channel (`pure.mode=scsi/fc`) or NVMe over Fibre Channel (`pure.mode=nvme/fc`), complete with automatic initiator WWPN discovery and volume registration.

- Documentation: {ref}`storage-pure`
- API extensions: {ref}`extension-storage-driver-pure-scsifc` and {ref}`extension-storage-driver-pure-nvmefc`

### Dell PowerStore NVMe support

The Dell PowerStore storage driver now supports NVMe over TCP (`nvme/tcp`) and NVMe over Fibre Channel (`nvme/fc`) transport modes.

NVMe/TCP is now configured as the default connectivity mode when creating new PowerStore storage pools without an explicit `powerstore.mode` setting.

- Documentation: {ref}`storage-powerstore`
- API extension: {ref}`extension-storage-driver-powerstore-nvme`

### Identity effective groups and state endpoint

A new identity state API endpoint and `recursion=2` listing mode have been introduced to provide visibility into effective group memberships.

The endpoint computes and returns the `effective_groups` for an identity, reporting the union of direct authorization groups and mapped OIDC identity provider groups.

- Documentation: {ref}`authentication`
- API extension: {ref}`extension-access-management-identity-effective-groups`

### Credential expiry reporting

The `Identity` API struct now includes an `expires_at` field across all identity listing and inspection endpoints.

This exposes the expiration date of client TLS certificates and issued bearer tokens, enabling proactive rotation and monitoring of client credentials before expiry.

- Documentation: {ref}`authentication`
- API extension: {ref}`extension-access-management-expiry`

### Pending states for bearer identities

LXD introduces pending identity types (`Client token bearer (pending)`, `DevLXD token bearer (pending)`, and `Initial UI token bearer (pending)`).

Bearer identities are created in a pending state until a valid token is issued, and automatically revert to the pending type if their token is revoked, providing clear lifecycle tracking for bearer access.

- Documentation: {ref}`authentication`
- API extension: {ref}`extension-access-management-bearer-pending`

### Durable operations

A new durable operation class has been introduced for mission-critical daemon workflows.

Durable operations persist their state in the cluster database.
If the cluster member executing the operation fails or goes offline, the operation is automatically resumed on the DQLite raft leader once heartbeat timeouts elapse.

- Documentation: {ref}`rest-api`
- API extension: {ref}`extension-durable-operations`

### Dedicated server state endpoint

A dedicated `GET /1.0/state` endpoint has been added to retrieve server status.
This API endpoint is only available when LXD is in standalone mode.
When clustered, the `GET /1.0/cluster/members/{name}/state` endpoint should be called.

This allows clients and monitoring tools to quickly inspect system and storage pool state.

- Documentation: {ref}`rest-api`
- API extension: {ref}`extension-server-state`

### CDI passthrough for NVIDIA MIG GPUs

GPU passthrough for `gputype=mig` devices has transitioned to the Container Device Interface (CDI) instead of legacy `nvidia.runtime` mechanisms.

Administrators can supply CDI identifiers directly in the device's `id` property or continue specifying `mig.uuid` or `mig.gi`/`mig.ci` pairs, which LXD resolves automatically to CDI devices via NVML.

- Documentation: {ref}`devices-gpu`
- API extension: {ref}`extension-gpu-mig-cdi`

### Operation child count and child listing

The `Operation` struct now includes a `child_count` field, allowing callers to determine whether an operation has spawned child operations without issuing recursive queries.

In addition, the `lxc operation` command now includes a `list-children` subcommand.

- Documentation: {ref}`rest-api`
- API extension: {ref}`extension-operation-child-count`

### Operation wait status code reporting

The operation wait endpoints now return the failed operation state and error status code upon failure.

This ensures callers and DevLXD clients receive immediate and accurate error diagnostics when long-running background tasks terminate with an error.

- Documentation: {ref}`rest-api`
- API extension: {ref}`extension-operation-wait-status-code`

### VM volatile max vCPUs tracking

Virtual machines now track maximum configured vCPUs in volatile configuration keys.

This preserves CPU hotplug boundaries across instance life cycles and enables live migration of virtual machines between cluster members with differing CPU core counts.

- Documentation: {ref}`instances`
- API extension: {ref}`extension-vm-volatile-maxcpus`

### Loki configuration readiness check

LXD now optionally skips Loki API endpoint readiness checks before attempting to transmit log streams.

This improves compatibility with OpenTelemetry (OTLP) and Canonical Observability Stack (COS) deployments.

- Documentation: {ref}`provided-metrics`
- API extension: {ref}`extension-loki-config-api-check-ready`

### Optional project replica mode

The `replica_mode` attribute is now omitted for projects that do not participate in disaster recovery replication topologies.

A new `lxc project clear-replica` command has also been added to clear replica state and promote/demote guard rails from projects.

- Documentation: {ref}`projects`
- API extension: {ref}`extension-project-replica-mode-optional`

(ref-release-notes-6.10.0-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`Fix infinite loop in network error log writer`](https://github.com/canonical/lxd/pull/18577)
- [{spellexception}`Fix physical network parent sharing across VLANs in clustered mode`](https://github.com/canonical/lxd/pull/18608)
- [{spellexception}`Improve duplicate cluster link error message`](https://github.com/canonical/lxd/pull/18626)
- [{spellexception}`Enforce project restrictions during instance move`](https://github.com/canonical/lxd/pull/18605)
- [{spellexception}`Fix failed file operation due to deleted forkfile as a result of race condition`](https://github.com/canonical/lxd/pull/18654)
- [{spellexception}`Fix bcache device handling in system resources`](https://github.com/canonical/lxd/pull/18695)
- [{spellexception}`Fix unprotected concurrent write to operation metadata`](https://github.com/canonical/lxd/pull/18694)
- [{spellexception}`Acquire lock before reading read-only operation flag in lxd-agent`](https://github.com/canonical/lxd/pull/18698)
- [{spellexception}`Fix snapshot table layout in lxc info when no volume snapshots exist`](https://github.com/canonical/lxd/pull/18723)
- [{spellexception}`Fix booting Windows in BIOS boot mode`](https://github.com/canonical/lxd/pull/18732)
- [{spellexception}`Make powerstore.mode optional to prevent per-target error`](https://github.com/canonical/lxd/pull/18738)
- [{spellexception}`Fix SRIOV VF used count and total disk allocated in lxc info`](https://github.com/canonical/lxd/pull/18742)
- [{spellexception}`Prevent hang on VM filesystem attach with idmap set`](https://github.com/canonical/lxd/pull/18733)
- [{spellexception}`Fix Ceph cluster_name handling for QEMU disks`](https://github.com/canonical/lxd/pull/18743)
- [{spellexception}`Properly validate disk device propagation option`](https://github.com/canonical/lxd/pull/18744)
- [{spellexception}`Globally enforce that caller can view requested project`](https://github.com/canonical/lxd/pull/18440)
- [{spellexception}`Detect download cancellation with errors.Is in simplestreams client`](https://github.com/canonical/lxd/pull/18779)
- [{spellexception}`Fetch network state when filtering instances by IP address in lxc list`](https://github.com/canonical/lxd/pull/18765)
- [{spellexception}`Limit image download to trusted size`](https://github.com/canonical/lxd/pull/18778)
- [{spellexception}`Use numeric IPv6 zone for NDP neighbour probe`](https://github.com/canonical/lxd/pull/18796)
- [{spellexception}`Include server operations when listing operations in default project`](https://github.com/canonical/lxd/pull/18806)
- [{spellexception}`Prevent busy ZFS dataset from blocking rebuild`](https://github.com/canonical/lxd/pull/18807)
- [{spellexception}`Enhance subprocess handling to prevent PID reuse issues`](https://github.com/canonical/lxd/pull/18872)
- [{spellexception}`Fix STARTTLS peek blocking the accept loop in endpoint listeners`](https://github.com/canonical/lxd/pull/18819)
- [{spellexception}`Fix double-close in idmap shifting`](https://github.com/canonical/lxd/pull/18927)
- [{spellexception}`Fix LXCFS clean up logic and avoid generating new certificates on running instances`](https://github.com/canonical/lxd/pull/18894)
- [{spellexception}`Sanitize server-provided image file names in client`](https://github.com/canonical/lxd/pull/18940)
- [{spellexception}`Fix file descriptor cleanup race condition in unit tests`](https://github.com/canonical/lxd/pull/18969)
- [{spellexception}`Fix Accept cancellation race in endpoint listeners`](https://github.com/canonical/lxd/pull/18904)
- [{spellexception}`Allow volume security.shifted and directory shift when raw.idmap is set on VM`](https://github.com/canonical/lxd/pull/18918)
- [{spellexception}`Preserve protected configuration keys during replicator refresh`](https://github.com/canonical/lxd/pull/18938)
- [{spellexception}`Restrict image reuse to cached images with identical image source`](https://github.com/canonical/lxd/pull/18987)
- [{spellexception}`Fix stacked tmpfs mounts for shared mounts and migrate legacy devlxd path`](https://github.com/canonical/lxd/pull/18854)
- [{spellexception}`Run cluster link address refresh on standalone LXD servers`](https://github.com/canonical/lxd/pull/19008)
- [{spellexception}`Use member address for cluster link bootstrap`](https://github.com/canonical/lxd/pull/19018)
- [{spellexception}`Apply source file mode on target when pulling files via CLI`](https://github.com/canonical/lxd/pull/19068)
- [{spellexception}`Keep target volatile configuration keys on migration refresh`](https://github.com/canonical/lxd/pull/19082)
- [{spellexception}`Fix VM live migration between cluster members with different CPU counts`](https://github.com/canonical/lxd/pull/18880)
- [{spellexception}`Refuse to delete or rename a cluster link referenced by a project replica.cluster`](https://github.com/canonical/lxd/pull/19112)
- [{spellexception}`Fix handling of cluster joins during heartbeat rounds`](https://github.com/canonical/lxd/pull/19153)
- [{spellexception}`Rename VM filesystem volume during snapshot rename on LVM`](https://github.com/canonical/lxd/pull/19154)
- [{spellexception}`Distinguish unassigned LUN from LUN 0 in Pure Storage driver`](https://github.com/canonical/lxd/pull/19067)
- [{spellexception}`Fix image update and btrfs cleanup`](https://github.com/canonical/lxd/pull/19170)
- [{spellexception}`Enforce viewership when granting authorization entitlements`](https://github.com/canonical/lxd/pull/18934)
- [{spellexception}`Accept null properties field in image upload tokens`](https://github.com/canonical/lxd/pull/19185)
- [{spellexception}`Wait for snapshot device to disappear on unmount in ZFS`](https://github.com/canonical/lxd/pull/19172)
- [{spellexception}`Prevent refused replicator refresh from leaving source configuration on target`](https://github.com/canonical/lxd/pull/19130)
- [{spellexception}`Promote and demote mirrored project volumes on failover`](https://github.com/canonical/lxd/pull/18941)
- [{spellexception}`Guard standby project storage on mirrored Ceph pool`](https://github.com/canonical/lxd/pull/18929)
- [{spellexception}`Avoid session key races in HPE Alletra storage client`](https://github.com/canonical/lxd/pull/19181)
- [{spellexception}`Preserve all volume fields in storage driver Clone`](https://github.com/canonical/lxd/pull/19180)
- [{spellexception}`Associate advertised BGP prefixes with their owning peer session`](https://github.com/canonical/lxd/pull/19187)
- [{spellexception}`Keep cross-node operation websockets alive`](https://github.com/canonical/lxd/pull/19041)
- [{spellexception}`Use stored bytes for Ceph storage pool usage calculation`](https://github.com/canonical/lxd/pull/19207)
- [{spellexception}`Make cluster member restore conflict with ongoing evacuations`](https://github.com/canonical/lxd/pull/19178)
- [{spellexception}`Fix backup handling and corruption safeguards`](https://github.com/canonical/lxd/pull/19175)
- [{spellexception}`Increase and pool migration and websocket transfer buffers`](https://github.com/canonical/lxd/pull/19208)

(ref-release-notes-6.10.0-incompatible)=
## Backwards-incompatible changes

These changes are not compatible with older versions of LXD or its clients.

(ref-release-notes-6.10.0-deprecated-nvidia-instance-configuration)=
### NVIDIA legacy instance-level configuration removed

The legacy instance-level NVIDIA configuration options (`nvidia.runtime`, `nvidia.driver.capabilities`, `nvidia.require.cuda`, and `nvidia.require.driver`) have been removed.

In addition, the `nvidia_runtime` and `nvidia_runtime_config` API extensions have been removed.
Existing instances, instance snapshots, and profiles will have these legacy options cleaned up automatically on daemon upgrade.

NVIDIA GPU passthrough should now be configured using the Container Device Interface (CDI) passthrough.

- Documentation: {ref}`devices-gpu`

(ref-release-notes-6.10.0-deprecated-cluster-healing)=
### Cluster healing functionality removed

The cluster healing feature and its server configuration setting `cluster.healing_threshold` have been removed.

In addition, the `cluster_healing` API extension has been removed.
Automatic evacuation and eviction of unresponsive cluster members has been discontinued to avoid accidental quorum loss during transient network partitions.

(ref-release-notes-6.10.0-go)=
## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.27.1.

(ref-release-notes-6.10.0-snap)=
## Snap packaging changes

- Shipped `qemu-nbd` and `qemu-storage-daemon` binaries to support persistent changed block tracking.
- Shipped `virtiofsd` on all architectures with virtual machine support (enabling non-amd64 architectures, except armhf).
- QEMU bumped to version 10.2.1+ds-1ubuntu3.2.
- EDK2 firmware bumped to 2025.11-3ubuntu7.2, shipping both Secure Boot and non-Secure Boot firmware variants.
- Reintroduced the KVM vAPIC option ROM for 32-bit BIOS guests in QEMU.
- Bumped ZFS to versions 2.2.11, 2.3.9, and 2.4.4 across respective branches.
- Bumped NVIDIA container toolkit and libnvidia-container to v1.20.1.
- Bumped LXC and LXCFS to v7.0.0 LTS.
- Enabled building LXD UI and documentation on riscv64.

(ref-release-notes-6.10.0-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-6.9...lxd-6.10.0).

(ref-release-notes-6.10.0-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-6.10.0).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=6/stable`
- **macOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
