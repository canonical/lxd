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
  Infrastructure as code ({abbr}`IaC`) can also facilitate the consistent deployment and configuration of LXD and required infrastructure.

Recovery point objective ({abbr}`RPO`)
: The maximum acceptable amount of time since the last data recovery point.
  The RPO determines expectations for an acceptable loss of data.

Recovery time objective ({abbr}`RTO`)
: The maximum acceptable delay between the interruption of services and restoration of service.
  The RTO determines an acceptable length of time for service downtime.

(exp-disaster-recovery-approaches)=
## Active-passive disaster recovery

LXD supports two approaches to active-passive disaster recovery:

LXD replicators
: Replicators are LXD entities that use cluster links to periodically copy instances from a project on one cluster to a project on another cluster.
  In the event of a disaster at the primary location, you can manage failover to the secondary cluster through LXD.
  Once the primary cluster comes back online, you can use replicators to restore the original replication direction.

Storage replication
: Data replication at the storage layer is possible when using remote storage drivers that support volume recovery.
  In this setup, you must configure replication separately from LXD, through the remote storage provider.
  Likewise, in the event of a disaster, you must manage promotion of the secondary cluster through the remote storage provider.

| | Replicators | Storage replication |
|---|---|---|
| **Level** | LXD instance layer | Storage array layer |
| **Data replication** | Incremental instance refresh over cluster links | Vendor storage replication, such as Ceph RBD mirroring or PowerFlex RCG |
| **Scheduling** | Controlled by LXD ({config:option}`replicator-conf:schedule` config key) | Controlled by the storage vendor |
| **Requires cluster link** | Yes | No |
| **Recovery method** | Promote standby project with LXD CLI or UI | Promote storage array, then use the `lxd recover` command |
| **Snapshot support** | Automatic pre-replication snapshots | Depends on storage vendor |

(exp-replicators)=
## Replicators

You can use LXD replicators to manage replication, failover, and failback end-to-end with LXD, without dependency on a specific storage backend.

(exp-replicators-concepts)=
### Leader and standby projects

Replication is configured at the project level.
You must create projects with the same name on both clusters, and then configure the replica mode of each project:

- `leader`: The project is writable.
  Instances in this project are the source of replication.
  The replicator runs from the cluster with this project.
- `standby`: Instances in this project are replicas, kept in sync by the replicator.
  New instances cannot be created directly in this project, and existing instances cannot be started.
  The project must be promoted to `leader` during a failover before instances can be started.
- (empty): The project is not part of any replication setup.
  This is the default for new projects.

The replica mode is not a configuration key.
For details about the dedicated CLI commands and UI processes you must use to manage the replica mode, refer to {ref}`howto-replicators-dr`.

You must also set the {config:option}`project-replica:replica.cluster` configuration key on the standby cluster to identify the cluster link that is allowed to push replication data to the project.
The leader project does not need this key because the replicator defines the target cluster.

The leader project pushes its instances to the standby project over the cluster link.
The standby project mirrors the leader at the time of the last replicator run.

(exp-replicators-how)=
### How replication works

When a replicator runs, LXD performs an incremental refresh of every instance in the leader project to the standby project, together with the custom storage volumes attached only to that instance.
Instances and volumes that do not yet exist on the standby are created; existing ones are updated to match the leader's current state.
A custom volume attached to more than one instance is not replicated and must exist on the standby before the instances using it can be replicated.
A volume attached through a profile is replicated when one instance alone uses it.
Because a profile device must point at an existing volume, create the volume on the standby and add the device to the standby's copy of the profile before the first run; the run then refreshes it.
Without the device the run refuses the instance before any data is sent.
Custom volumes are only replicated when the project has `features.storage.volumes=true`; projects that inherit volumes from the default project have no project-local custom volumes to replicate.

Before each refresh, LXD creates a point-in-time snapshot of each instance on the leader, capturing its root disk and the custom volumes attached only to it at the same moment.
This provides a consistent rollback point on the source cluster in case anything goes wrong during replication, and the volume snapshots travel to the standby with the volumes.
The exception is instances that already have a {config:option}`instance-snapshots:snapshots.schedule` configured and no custom volume attached: their scheduled snapshots already provide point-in-time history, so LXD skips the extra snapshot to avoid redundancy.
Scheduled snapshots capture the root disk alone, so an instance with custom volumes always gets a snapshot here.

Replication can be triggered manually through the LXD CLI or UI, or scheduled automatically using a cron expression in the {config:option}`replicator-conf:schedule` configuration key.

(exp-replicators-failover)=
### Failover and recovery

If the leader cluster fails, the standby project can be promoted through the LXD CLI or UI.
This makes the project writable and allows instances to be started.
If the leader cluster is unreachable, validation against it is skipped automatically.
You can "force" promote the project to skip all validation without attempting to connect, which is useful when the leader is known to be down or during a planned takeover.

When the primary cluster comes back online, you can synchronize the projects by running the replicator in "restore" mode; then you can demote the project on the secondary cluster and promote the project on the primary cluster to return the projects to their original roles.
In restore mode, the instance list on the secondary cluster is used as the authoritative source: instances that were created on the secondary cluster after failover are also created on the recovering cluster, not just the instances that existed before the failure.

See {ref}`howto-replicators-dr` for step-by-step instructions.

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
Some {ref}`remote storage drivers <storage-drivers-remote>`, however, such as the {ref}`PowerFlex <storage-powerflex>`, {ref}`PowerStore <storage-powerstore>`, and {ref}`Pure <storage-pure>` drivers, use transformed volume names, and the recovery tool is unable to discover these volumes from their name alone.
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

Reference:

- {ref}`ref-replicator-config`
- {ref}`exp-cluster-links`
