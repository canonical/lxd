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

Replication is configured at the project level. Both clusters have a project with the same name, and each project has a replica mode:

- `leader`: The project is writable. Instances in this project are the source of replication. The replicator runs from this cluster.
- `standby`: Instances in this project are replicas, kept in sync by the replicator. New instances cannot be created directly in this project, and existing instances cannot be started. The project must be promoted to `leader` during a failover before instances can be started.
- (empty): The project is not part of any replication setup. This is the default for new projects.

Replica mode is managed via `lxc project promote-replica`, `lxc project demote-replica`, and `lxc project clear-replica` (which resets the replica mode back to empty). It is not a configuration key and cannot be set with `lxc project set`.

Clearing the replica mode of a standby project requires `--force`, because it drops the record of which cluster was replicating into it, and the project could then be promoted under the weaker rules that apply to a project taking no part in replication.

The {config:option}`project-replica:replica.cluster` configuration key identifies the cluster link that is allowed to push replication data into a standby project. It is required on the standby project, and it must also be set on the leader project if you intend to fail over and later return to the original replication direction: after a failover the original leader becomes a standby, and it can only be promoted back to leader once LXD can identify the cluster it was replicating with.

A project can only be promoted or demoted if it takes part in a replication topology. Demoting requires the `replica.cluster` key, because a standby project cannot accept replication data without it. Promoting a project that has no replica mode set requires at least one replicator, and promoting a standby requires the `replica.cluster` key, which is what identifies the cluster whose project must have stepped down first. Use `--force` to override these checks.

To swap the roles of two clusters in a planned switchover, demote the current leader first, then promote the standby. Demoting first is always safe: the topology is briefly left without a leader, which only pauses writes, whereas promoting first would leave two clusters accepting writes at the same time.

The leader project pushes its instances to the standby project over the cluster link. The standby project mirrors the leader at the time of the last replicator run.

(exp-replicators-how)=
## How replication works

When a replicator runs, LXD performs an incremental refresh of every instance in the leader project to the standby project. Instances that do not yet exist on the standby are created; existing instances are updated to match the leader's current state.

Before each refresh, LXD creates a point-in-time snapshot of each instance on the leader. This provides a consistent rollback point on the source cluster in case anything goes wrong during replication. The exception is instances that already have a {config:option}`instance-snapshots:snapshots.schedule` configured: their scheduled snapshots already provide point-in-time history, so LXD skips the extra snapshot to avoid redundancy.

Replication can be triggered manually with `lxc replicator run`, or scheduled automatically using a cron expression in the {config:option}`replicator-conf:schedule` configuration key.

(exp-replicators-failover)=
## Failover and recovery

If the leader cluster fails, the standby project can be promoted with `lxc project promote-replica`. This makes the project writable and allows instances to be started. If the leader cluster is unreachable, validation against it is skipped automatically. Use `--force` to skip all validation without attempting to connect, which is useful when the leader is known to be down. Do not use it for a planned switchover: demote the leader first, then promote the standby.

When the original leader comes back online, it can be re-synced from the new leader by running the replicator in restore mode (`lxc replicator run --restore`), then returning both projects to their original roles with `lxc project demote-replica` and `lxc project promote-replica`. In restore mode, the remote leader's instance list is used as the authoritative source: instances that were created on the new leader after failover are also created on the recovering cluster, not just the instances that existed before the failure.

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
| **Recovery method** | Promote standby project with `lxc project promote-replica` | Promote storage array, then run `lxd recover` |
| **Snapshot support** | Automatic pre-replication snapshots | Depends on storage vendor |

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
