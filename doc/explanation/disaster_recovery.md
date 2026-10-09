---
myst:
  html_meta:
    description: "An explanation of LXD disaster recovery strategies, including active-passive disaster recovery with replicators and with storage replication."
---

(exp-disaster-recovery)=
# Disaster recovery

LXD provides support for different approaches to active-passive disaster recovery: a strategy that allows for the restoration of workloads on a secondary cluster after a disaster.

```{note}
Backups of standalone LXD servers can also protect against data loss.
For details about different backup methods, see {ref}`backups`.
```

(exp-disaster-recovery-concepts)=
## Disaster recovery concepts

Active-passive disaster recovery
: This strategy requires the deployment of two LXD clusters with the same infrastructure and configuration.
  Under this setup, a primary (active) cluster manages workloads, and a secondary (passive) cluster only becomes active if the primary cluster fails.
  Preparation for active-passive disaster recovery requires the periodic replication of data from the primary cluster on the secondary cluster.
  Infrastructure as code tools ({abbr}`IaC`) can also facilitate the consistent deployment and configuration of LXD and required infrastructure.
  The [LXD Terraform provider](https://registry.terraform.io/providers/terraform-lxd/lxd/latest/docs), for example, is an IaC tool that manages LXD resources.

Recovery point objective ({abbr}`RPO`)
: The maximum acceptable amount of time since the last data recovery point.
  The RPO determines expectations for an acceptable loss of data.

Recovery time objective ({abbr}`RTO`)
: The maximum acceptable delay between the interruption of services and restoration of service.
  The RTO determines an acceptable length of time for service downtime.

(exp-disaster-recovery-monitoring)=
## Monitoring

LXD collects metrics and streams events.
Setting up external observability systems in advance to collect this data can assist with disaster detection and evaluation.
Refer to the {ref}`metrics reference <provided-metrics>` and {ref}`events reference <events>` for details about the data produced by LXD.
For example, {ref}`replicator-metrics` can be used to determine the current RPO of clusters that use replicators for data replication.
For information about how to gather and store metrics and logs, see {ref}`metrics`, {ref}`logs_loki`, and {ref}`grafana`.

(exp-disaster-recovery-approaches)=
## Active-passive disaster recovery

LXD supports two approaches to active-passive disaster recovery:

LXD replicators
: Replicators are LXD entities that use cluster links to periodically copy instances from a project on one cluster to a project on another cluster.
  In the event of a disaster at the primary location, you can manage failover to the secondary cluster through LXD.
  Once the primary cluster comes back online, you can use replicators to restore the original replication direction.
  On Ceph RBD storage pools, replicators can also use {ref}`Ceph RBD mirroring <exp-replicators-ceph>` to copy the data.

Storage replication
: Data replication at the storage layer is possible when using remote storage drivers that support volume recovery.
  In this setup, you must configure replication separately from LXD, through the remote storage provider.
  Likewise, in the event of a disaster, you must manage promotion of the secondary cluster through the remote storage provider.

| | Replicators | Storage replication |
|---|---|---|
| **Level** | LXD instance layer (or storage layer for the data, when Ceph RBD mirroring is configured) | Storage array layer |
| **Data replication** | Incremental instance refresh over cluster links (or Ceph RBD mirroring triggered by LXD) | Vendor storage replication, such as Ceph RBD mirroring or PowerFlex RCG |
| **Scheduling** | Controlled by LXD ({config:option}`replicator-conf:schedule` config key) | Controlled by the storage vendor |
| **Requires cluster link** | Yes | No |
| **Recovery method** | Promote standby project with LXD CLI or UI | Promote storage array, then use the `lxd recover` command |
| **Snapshot support** | Automatic pre-replication snapshots | Depends on storage vendor |

(exp-replicators)=
## Replicators

You can use LXD replicators to manage replication, failover, and failback end-to-end with LXD, without dependency on a specific storage backend.
On Ceph RBD storage pools, you can set up a replicator to use Ceph RBD mirroring to copy the data.
The replicator then sends only the instance and volume records over the cluster link.

(exp-replicators-concepts)=
### Leader and standby projects

Replication is configured at the project level. You must create projects with the same name on both clusters, and then configure the replica mode of each project:

- `leader`: The project is writable.
  Instances in this project are the source of replication.
  The replicator runs from the cluster with this project.
- `standby`: Instances in this project are replicas, kept in sync by the replicator.
  New instances cannot be created directly in this project, and existing instances cannot be started.
  The project must be promoted to `leader` during a failover before instances can be started.
- (empty): The project is not part of any replication setup.
  This is the default for new projects.

The replica mode is not a configuration key. For details about the dedicated CLI commands and UI processes you must use to manage the replica mode, refer to {ref}`howto-replicators-dr`.

Clearing the replica mode of a standby project must be forced, because it drops the record of which cluster was replicating into it. Once the replica mode is unset, the project could then be promoted without checking that the cluster replicating into the project has stepped down. If that cluster's project is still in `leader` mode, both projects become writable at the same time, and changes made independently on each side diverge and cannot be reconciled by the next replicator run.

The {config:option}`project-replica:replica.cluster` configuration key identifies the cluster link that is allowed to push replication data into a standby project. It is required on the standby project, and it must also be set on the leader project if you intend to fail over and later return to the original replication direction: after a failover the original leader becomes a standby, and it can only be promoted back to leader once LXD can identify the cluster it was replicating with.

A project can only be promoted or demoted if it takes part in a replication topology. Demotion requires the `replica.cluster` key, because a standby project cannot accept replication data without it. Promoting a project that has no replica mode set requires at least one replicator, and promoting a standby requires the `replica.cluster` key, which is what identifies the cluster whose project must have stepped down first. Forced promotion and demotion override these checks.

To swap the roles of two clusters in a planned switchover, demote the current leader first, then promote the standby. Demoting first is always safe: the topology is briefly left without a leader, which only pauses writes; promoting first, however, would allow both clusters to accept writes at the same time.

The leader project pushes its instances to the standby project over the cluster link.
The standby project mirrors the leader at the time of the last replicator run.

(exp-replicators-how)=
### How replication works

When a replicator runs, LXD performs an incremental refresh of every instance in the leader project to the standby project, together with the custom storage volumes attached only to that instance. Instances and volumes that do not yet exist on the standby are created; existing ones are updated to match the leader's current state. A custom volume attached to more than one instance is not replicated and must exist on the standby before the instances using it can be replicated. A volume attached through a profile is replicated when one instance alone uses it. Because a profile device must point at an existing volume, create the volume on the standby and add the device to the standby's copy of the profile before the first run; the run then refreshes it. Without the device the run refuses the instance before any data is sent. Custom volumes are only replicated when the project has `features.storage.volumes=true`; projects that inherit volumes from the default project have no project-local custom volumes to replicate.

Before each refresh, LXD creates a point-in-time snapshot of each instance on the leader, capturing its root disk and the custom volumes attached only to it at the same moment. This provides a consistent rollback point on the source cluster in case anything goes wrong during replication, and the volume snapshots travel to the standby with the volumes. The exception is instances that already have a {config:option}`instance-snapshots:snapshots.schedule` configured and no custom volume attached: their scheduled snapshots already provide point-in-time history, so LXD skips the extra snapshot to avoid redundancy. Scheduled snapshots capture the root disk alone, so an instance with custom volumes always gets a snapshot here.

Replication can be triggered manually through the LXD CLI or UI, or scheduled automatically using a cron expression in the {config:option}`replicator-conf:schedule` configuration key.

(exp-replicators-failover)=
### Failover and recovery

If the leader cluster fails, the standby project can be promoted through the LXD CLI or UI. This makes the project writable and allows instances to be started. If the leader cluster is unreachable, validation against it is skipped automatically. Forced promotion skips all validation without attempting to connect, which is useful when the leader is known to be down. Do not use it for a planned switchover: demote the leader first, then promote the standby.

When the primary cluster comes back online, you can synchronize the projects by running the replicator in "restore" mode; then you can demote the project on the secondary cluster and promote the project on the primary cluster to return the projects to their original roles. In restore mode, the instance list on the secondary cluster is used as the authoritative source: instances that were created on the secondary cluster after failover are also created on the recovering cluster, not just the instances that existed before the failure.

See {ref}`howto-replicators-dr` for step-by-step instructions.

(exp-replicators-ceph)=
### Replicators with Ceph RBD mirroring

By default, a replicator copies the data of every instance over the cluster link.
If both clusters keep the project on {ref}`Ceph RBD <storage-ceph>` storage pools, Ceph can copy the data instead, through [RBD mirroring](https://docs.ceph.com/en/reef/rbd/rbd-mirroring/) between the two Ceph clusters.
LXD still manages replication, failover, and failback, so you do not need to promote volumes in Ceph or use the `lxd recover` command after a disaster.

Use Ceph RBD mirroring when the two Ceph clusters are connected by a dedicated high-speed, low-latency link.
The data moves over that link, and the cluster link carries only the instance and volume records.
The commands are the same as for any replicator: you create and run the replicator, and promote and demote the project in LXD.
Setting up the mirroring in Ceph is the only extra step.

You enable this behavior per project with the {config:option}`ceph.replicator <storage-ceph-pool-conf:ceph.replicator.<project>>` storage pool configuration key, which names the peer Ceph site.
A project with this key set on one of its storage pools is a mirrored project.

When a replicator runs for a mirrored project, LXD:

1. Creates the pre-replication snapshots, as for any replicator.
1. Enrolls each volume that the project holds on the storage pool in RBD mirroring, and triggers a mirror snapshot of each volume.
1. Waits until the peer Ceph site confirms that it has received every mirror snapshot.
1. Sends the records of the instances, and of the custom volumes attached only to one instance, to the standby project over the cluster link, without the data.

The first run copies every volume in full.
Later runs copy only the changes since the previous mirror snapshot.

The secondary cluster treats the volumes of a mirrored standby project as replicas that belong to Ceph.
It keeps their records in its database, but it does not create, change, or delete them on storage.

(exp-replicators-ceph-failover)=
#### Failover and failback with Ceph RBD mirroring

When you promote a mirrored standby project, LXD also promotes its volumes in Ceph, so that instances can be started.
When you demote a mirrored leader project, LXD also demotes its volumes, so that the other cluster can take over.
All instances in the project must be stopped before you demote it.

If the primary cluster fails, it cannot demote its volumes.
In this case, you must "force" promote the standby project.
The instances then start from the last mirror snapshot that reached the secondary cluster.

After a forced promotion, the volumes on the two clusters diverge.
When the primary cluster comes back online, you must demote its project twice.
The first demotion makes its volumes read-only, and Ceph then reports that they have diverged.
The second demotion discards the diverged volumes on the primary cluster, and replaces them with volumes copied from the secondary cluster.
"Restore" mode is not available for a replicator on a mirrored project.
Instead, you must create a replicator on the secondary cluster that targets the primary cluster to synchronize LXD records.

Because the two clusters must agree on which one holds the writable volumes, do not force promote a mirrored project while the primary cluster is still available.
Demote the leader project first, and then promote the standby project without force.

See {ref}`howto-replicators-setup` for the setup and {ref}`howto-replicators-dr` for step-by-step instructions.

(exp-storage-replication)=
## Storage replication

Replication at the storage array layer is possible with remote storage drivers that support volume recovery.
You must configure replication outside of LXD, through a process that depends on the remote storage vendor.

For detailed instructions and information about storage providers that support storage replication, refer to {ref}`disaster-recovery-replication`.

(exp-storage-failover)=
### Failover with storage replication

In the event of a disaster, you must manage promotion of the secondary cluster outside of LXD, through the remote storage provider.
Once you have promoted the secondary cluster, you can use the LXD recovery tool (`lxd recover`) to recover instances and custom volumes from the replicated data.
Infrastructure as code ({abbr}`IaC`) can facilitate redeployment of the original resources and configuration.

For instructions on how to use the recovery tool, see {ref}`disaster-recovery`.

(exp-disaster-recovery-lxd-recover)=
## LXD database recovery with `lxd recover`

The LXD recovery tool, `lxd recover`, facilitates recovery of LXD instances and custom volumes when a LXD database is lost or corrupted.
You can also use this tool to recover resources when {ref}`performing disaster recovery with storage replication <disaster-recovery-replication>`.

When you run `lxd recover`, the recovery tool scans all storage pools that exist in the database and identifies missing volumes that can be recovered.
The tool also scans volumes on the known storage pools and, in the process, may discover additional storage pools that exist on disk but are missing from the LXD database.
In such cases, the tool prints information about the storage pools so that you can re-create their database records manually.
Concrete recovery examples for each storage driver can be found in {ref}`howto-storage-pools-recover`.
The tool then mounts any unmounted storage pools and continues scanning for volumes that may be associated with LXD.

Through this scan, the recovery tool can identify some custom volumes by name.
Some {ref}`remote storage drivers <storage-drivers-remote>`, however, such as the {ref}`PowerFlex <storage-powerflex>`, {ref}`PowerStore <storage-powerstore>`, and {ref}`Everpure <storage-pure>` drivers, use transformed volume names, and the recovery tool is unable to discover these volumes from their name alone.
Instead, these volumes can only be discovered if they are attached to an instance.

LXD maintains a `backup.yaml` file in each instance's storage volume, which contains all necessary information to recover a given instance.
The recovery tool compares the `backup.yaml` file with what is actually on disk (such as matching snapshots) and, if this consistency check passes, re-creates the database records.
The tool can also use the `backup.yaml` file to gather information about profiles, storage pools, and attached devices (such as storage volumes with transformed names).
Based on this information, the tool will prompt you to re-create missing entities, but it will not display information about how those entities were configured, unless they are storage pools.
For example, if an instance used a bridge network attached to a profile, the tool will notify you that the two entities are missing from the database, but it will not direct you to attach the network to the profile.

## Related topics

How-to guides:

- {ref}`howto-replicators-setup`
- {ref}`howto-replicators-manage`
- {ref}`howto-replicators-dr`
- {ref}`disaster-recovery-replication`
- {ref}`disaster-recovery`
- {ref}`metrics`
- {ref}`logs_loki`
- {ref}`grafana`

Reference:

- {ref}`ref-replicator-config`
- {ref}`exp-cluster-links`
- {ref}`provided-metrics`
- {ref}`events`
