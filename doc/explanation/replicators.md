---
myst:
  html_meta:
    description: An explanation of LXD replicators and how they enable active-passive disaster recovery across cluster links.
---

(exp-replicators)=
# Replicators

Replicators are LXD entities that periodically copy instances from one cluster to another across a {ref}`cluster link <exp-cluster-links>`. They are designed for active-passive disaster recovery, where a leader cluster runs all workloads and a standby cluster stays ready to take over if the leader fails.
On Ceph RBD storage pools, a replicator can let Ceph RBD mirroring copy the data, and send only the instance and volume records over the cluster link.

(exp-replicators-concepts)=
## Leader and standby projects

Replication is configured at the project level. Both clusters have a project with the same name, and each project has a replica mode:

- `leader`: The project is writable. Instances in this project are the source of replication. The replicator runs from this cluster.
- `standby`: Instances in this project are replicas, kept in sync by the replicator. New instances cannot be created directly in this project, and existing instances cannot be started. The project must be promoted to `leader` during a failover before instances can be started.
- (empty): The project is not part of any replication setup. This is the default for new projects.

Replica mode is managed via `lxc project promote-replica`, `lxc project demote-replica`, and `lxc project clear-replica` (which resets the replica mode back to empty). It is not a configuration key and cannot be set with `lxc project set`.

Only the standby project needs the {config:option}`project-replica:replica.cluster` configuration key, which identifies the cluster link that is allowed to push replication data into it. The leader project does not need this key because the replicator defines the target cluster.

The leader project pushes its instances to the standby project over the cluster link. The standby project mirrors the leader at the time of the last replicator run.

(exp-replicators-how)=
## How replication works

When a replicator runs, LXD performs an incremental refresh of every instance in the leader project to the standby project, together with the custom storage volumes attached only to that instance. Instances and volumes that do not yet exist on the standby are created; existing ones are updated to match the leader's current state. A custom volume attached to more than one instance is not replicated and must exist on the standby before the instances using it can be replicated. A volume attached through a profile is replicated when one instance alone uses it. Because a profile device must point at an existing volume, create the volume on the standby and add the device to the standby's copy of the profile before the first run; the run then refreshes it. Without the device the run refuses the instance before any data is sent. Custom volumes are only replicated when the project has `features.storage.volumes=true`; projects that inherit volumes from the default project have no project-local custom volumes to replicate.

Before each refresh, LXD creates a point-in-time snapshot of each instance on the leader, capturing its root disk and the custom volumes attached only to it at the same moment. This provides a consistent rollback point on the source cluster in case anything goes wrong during replication, and the volume snapshots travel to the standby with the volumes. The exception is instances that already have a {config:option}`instance-snapshots:snapshots.schedule` configured and no custom volume attached: their scheduled snapshots already provide point-in-time history, so LXD skips the extra snapshot to avoid redundancy. Scheduled snapshots capture the root disk alone, so an instance with custom volumes always gets a snapshot here.

Replication can be triggered manually with `lxc replicator run`, or scheduled automatically using a cron expression in the {config:option}`replicator-conf:schedule` configuration key.

(exp-replicators-failover)=
## Failover and recovery

If the leader cluster fails, the standby project can be promoted with `lxc project promote-replica`. This makes the project writable and allows instances to be started. If the leader cluster is unreachable, validation against it is skipped automatically. Use `--force` to skip all validation without attempting to connect, which is useful when the leader is known to be down or during a planned takeover.

When the original leader comes back online, it can be re-synced from the new leader by running the replicator in restore mode (`lxc replicator run --restore`), then returning both projects to their original roles with `lxc project demote-replica` and `lxc project promote-replica`. In restore mode, the remote leader's instance list is used as the authoritative source: instances that were created on the new leader after failover are also created on the recovering cluster, not just the instances that existed before the failure.

See {ref}`howto-replicators-dr` for step-by-step instructions.

(exp-replicators-ceph)=
## Replicators with Ceph RBD mirroring

By default, a replicator copies the data of every instance over the cluster link.
If both clusters keep the project on {ref}`Ceph RBD <storage-ceph>` storage pools, Ceph can copy the data instead, through [RBD mirroring](https://docs.ceph.com/en/reef/rbd/rbd-mirroring/) between the two Ceph clusters.
LXD still manages replication, failover, and failback, so you do not need to promote volumes in Ceph or use the `lxd recover` command after a disaster.

You enable this behavior per project with the {config:option}`ceph.replicator <storage-ceph-pool-conf:ceph.replicator.<project>>` storage pool configuration key, which names the peer Ceph site.
A project with this key set on one of its storage pools is a mirrored project.

When a replicator runs for a mirrored project, LXD:

1. Creates the pre-replication snapshots, as for any replicator.
1. Enrolls each volume that the project holds on the storage pool in RBD mirroring, and triggers a mirror snapshot of each volume.
1. Waits until the peer Ceph site confirms that it has received every mirror snapshot.
1. Sends the records of the instances, and of the custom volumes attached only to one instance, to the standby project over the cluster link, without the data.

The first run copies every volume in full.
Later runs copy only the changes since the previous mirror snapshot.

The standby cluster treats the volumes of a mirrored standby project as replicas that belong to Ceph.
It keeps their records in its database, but it does not create, change, or delete them on storage.

(exp-replicators-ceph-failover)=
### Failover and recovery with Ceph RBD mirroring

When you promote a mirrored standby project, LXD also promotes its volumes in Ceph, so that instances can be started.
When you demote a mirrored leader project, LXD also demotes its volumes, so that the other cluster can take over.
All instances in the project must be stopped before you demote it.

If the leader cluster fails, it cannot demote its volumes.
In this case, you must "force" promote the standby project.
The instances then start from the last mirror snapshot that reached the standby cluster.

After a forced promotion, the volumes on the two clusters diverge.
When the original leader cluster comes back online, you must demote its project twice.
The first demotion makes its volumes read-only, and Ceph then reports that they have diverged.
The second demotion discards the diverged volumes and copies them again from the new leader cluster.
"Restore" mode is not available for a mirrored project.
Instead, you create a replicator on the new leader cluster that targets the original leader cluster.

Because the two clusters must agree on which one holds the writable volumes, do not force promote a mirrored project while the leader cluster is still available.
Demote the leader project first, and then promote the standby project without force.

See {ref}`howto-replicators-ceph` for the setup.

(exp-replicators-vs-storage-replication)=
## Replicators vs. storage replication

LXD supports two distinct approaches to cross-site disaster recovery:

| | Replicators | Replicators with Ceph RBD mirroring | Storage replication |
|---|---|---|---|
| **Level** | LXD instance layer | LXD instance layer for the records, Ceph for the data | Storage array layer |
| **Mechanism** | Incremental instance refresh over cluster links | Ceph RBD mirroring, triggered by LXD on every run | Vendor storage replication (Ceph RBD mirroring, PowerFlex RCG, etc.) |
| **Scheduling** | Controlled by LXD ({config:option}`replicator-conf:schedule` config key) | Controlled by LXD ({config:option}`replicator-conf:schedule` config key) | Controlled by the storage vendor |
| **Requires cluster link** | Yes | Yes | No |
| **Recovery method** | Promote standby project with `lxc project promote-replica` | Promote standby project with `lxc project promote-replica` | Promote storage array, then run `lxd recover` |
| **Snapshot support** | Automatic pre-replication snapshots | Automatic pre-replication snapshots | Depends on storage vendor |

Use replicators when you want LXD to manage replication end-to-end across two clusters without dependency on a specific storage backend. Use {ref}`storage replication <disaster-recovery-replication>` when you need replication at the storage array level, or when you are not using cluster links.
For Ceph RBD, LXD can also manage the replication itself, as described in {ref}`exp-replicators-ceph`.

## Related topics

How-to guides:

* {ref}`howto-replicators-setup`
* {ref}`howto-replicators-manage`
* {ref}`howto-replicators-dr`
* {ref}`disaster-recovery-replication`

Reference:

* {ref}`ref-replicator-config`
* {ref}`exp-cluster-links`
