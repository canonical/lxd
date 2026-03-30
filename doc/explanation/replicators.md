---
myst:
  html_meta:
    description: An explanation of LXD replicators and how they enable active-passive disaster recovery across cluster links.
---

(exp-replicators)=
# Replicators

Replicators are LXD entities that periodically copy instances from one cluster to another across a {ref}`cluster link <exp-cluster-links>`. They are designed for active-passive disaster recovery, where a leader cluster runs all workloads and a standby cluster stays ready to take over if the leader fails.

(exp-replicators-concepts)=
## Leader and standby projects

Replication is configured at the project level. Both clusters have a project with the same name, and each project is assigned a {config:option}`project-replica:replica.mode`:

- `leader`: The project is writable. Instances in this project are the source of replication. The replicator runs from this cluster.
- `standby`: Instances in this project are replicas, kept in sync by the replicator. New instances cannot be created directly in this project; existing instances can still be managed. The project must be promoted to `leader` during a failover before instances can be started.

The leader project pushes its instances to the standby project over the cluster link. The standby project mirrors the leader at the time of the last replicator run.

(exp-replicators-how)=
## How replication works

When a replicator runs, LXD performs an incremental refresh of every instance in the leader project to the standby project. Instances that do not yet exist on the standby are created; existing instances are updated to match the leader's current state.

If {config:option}`replicator-conf:snapshot` is set to `true` on the replicator, LXD creates a point-in-time snapshot of each instance on the leader before the refresh. This provides a consistent rollback point on the source cluster in case anything goes wrong during replication.

Replication can be triggered manually with `lxc replicator run`, or scheduled automatically using a cron expression in the {config:option}`replicator-conf:schedule` configuration key.

(exp-replicators-failover)=
## Failover and recovery

If the leader cluster fails, the standby project can be promoted by setting `replica.mode=leader` on the standby cluster. This makes the project writable and allows instances to be started.

When the original leader comes back online, it can be re-synced from the new leader by running the replicator in restore mode (`lxc replicator run --restore`), then returning both projects to their original roles. In restore mode, the remote leader's instance list is used as the authoritative source: instances that were created on the new leader after failover are also created on the recovering cluster, not just the instances that existed before the failure.

See {ref}`howto-replicators-dr` for step-by-step instructions.

(exp-replicators-vs-storage-replication)=
## Replicators vs. storage replication

LXD supports two distinct approaches to cross-site disaster recovery:

| | Replicators | Storage replication |
|---|---|---|
| **Level** | LXD instance layer | Storage array layer |
| **Mechanism** | Incremental instance refresh over cluster links | Vendor storage replication (Ceph RBD mirroring, PowerFlex RCG, etc.) |
| **Scheduling** | Controlled by LXD ({config:option}`replicator-conf:schedule` config key) | Controlled by the storage vendor |
| **Requires cluster link** | Yes | No |
| **Recovery method** | Promote standby project with `replica.mode=leader` | Promote storage array, then run `lxd recover` |
| **Snapshot support** | Optional pre-replication snapshots | Depends on storage vendor |

Use replicators when you want LXD to manage replication end-to-end across two clusters without dependency on a specific storage backend. Use {ref}`storage replication <disaster-recovery-replication>` when you need replication at the storage array level, or when you are not using cluster links.

## Related topics

How-to guides:

* {ref}`howto-replicators-setup`
* {ref}`howto-replicators-manage`
* {ref}`howto-replicators-dr`
* {ref}`disaster-recovery-replication`

Reference:

* {ref}`ref-replicator-config`
* {ref}`exp-cluster-links`
