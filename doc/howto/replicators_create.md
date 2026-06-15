---
myst:
  html_meta:
    description: How to set up replicators to sync instances across LXD cluster links for active-passive disaster recovery.
---

(howto-replicators-setup)=
# How to set up replicators

Replicators sync instances across LXD cluster links. This is useful for active-passive disaster recovery, where a leader (active) cluster handles all workloads while a standby cluster remains ready to take over if the leader fails.

LXD supports this strategy using project replicators over a {ref}`cluster link <exp-cluster-links>`.

(howto-replicators-prereqs)=
## Prerequisites

Before setting up replicators:

1. Two LXD clusters must be initialized. We will call them "leader" and "standby".
1. You need sufficient permissions on both clusters to establish links and manage projects.
1. A {ref}`cluster link must be established <howto-cluster-links-create>` between the two clusters.
1. Network connectivity must exist between the clusters.

(howto-replicators-auth)=
## Prepare authentication

Replicators communicate over cluster links, so the linked cluster identities must be granted the permissions they need on the replicated project. Configure these permissions using authentication groups and {ref}`manage-permissions`.

For project replication, the cluster-link identity on each cluster typically needs at least these permissions:

- `operator` on the replicated project, so it can perform instance replication work in that project
- `can_edit` on the replicated project, so replica project configuration can be validated and updated as part of the workflow

For example, if the replicated project is called `myproject`, you can prepare an authentication group on each cluster before creating the cluster links:

```bash
lxc auth group create replicators
lxc auth group permission add replicators project myproject operator
lxc auth group permission add replicators project myproject can_edit
```

Then create the cluster links with that authentication group, as described in {ref}`howto-cluster-links-create`.

(howto-replicators-project-setup)=
## Configure projects for replication

Both clusters need a project with the same name. Only the standby project requires the {config:option}`project-replica:replica.cluster` configuration key; the leader project does not need it because the replicator defines the target cluster.

1. On the leader cluster, create a project:

   ```bash
   lxc project create <project_name>
   ```

1. On the standby cluster, create a project with the same name and configure it to accept replication from the leader cluster:

   ```bash
   lxc project create <project_name> -c replica.cluster=<leader_cluster_link_name>
   ```

1. On the standby cluster, demote the project to standby mode. This prevents new instances from being created in the project and existing instances from being started. The project must be promoted to `leader` during a failover before instances can be started.

   ```bash
   lxc project demote-replica <project_name>
   ```

1. On the leader cluster, promote the project to leader mode:

   ```bash
   lxc project promote-replica <project_name>
   ```

```{admonition} Promote validation
:class: note

The `lxc project promote-replica` command validates that all target projects (on clusters referenced by the project's replicators) are in standby mode before allowing the promotion.
This ensures that new instances are not created on a standby cluster between replicator runs.
If a target cluster is unreachable, promotion still proceeds to allow disaster recovery scenarios where the target may be offline.
```

(howto-replicators-create)=
## Create a replicator

After configuring the projects on both clusters, create a replicator on the leader cluster. The `cluster` configuration key is required and must be set to the name of an existing cluster link.

Each cluster link can be targeted by at most one replicator per project. Creating or updating a replicator to target a cluster link already used by another replicator in the same project fails with a conflict error.

```bash
lxc replicator create <replicator_name> cluster=<standby_cluster_link_name> --project <project_name>
```

For example:

```bash
lxc replicator create my-replicator cluster=lxd-standby --project myproject
```

You can also create a replicator with a schedule and snapshot options:

```bash
lxc replicator create my-replicator cluster=lxd-standby schedule="@daily" snapshot=true --project myproject
```

See {ref}`ref-replicator-config` for all available configuration options.

(howto-replicators-run)=
## Run a replicator

To manually trigger a replicator run, use the following command on the leader cluster:

```bash
lxc replicator run <replicator_name>
```

This syncs all instances in the source project to the standby cluster.

To schedule replication automatically, set the `schedule` configuration key with a cron expression:

```bash
lxc replicator set <replicator_name> schedule="0 0 * * *"
```

(howto-replicators-snapshot)=
## Snapshot before replication

Each replicator run performs an incremental instance sync to the standby cluster using
the equivalent of `lxc copy --refresh`. This transfers only the data that has changed since the last sync,
using any existing snapshots as a reference point to minimize the amount of data transferred.

When you set `snapshot=true` on a replicator, LXD creates a point-in-time snapshot of each
source instance before performing the incremental copy. This gives the copy operation a
consistent reference point, which reduces the amount of data transferred on each sync and
provides a rollback point on the source in case anything goes wrong during replication.

Snapshot naming and expiry are controlled entirely by the instance's own configuration (for
example {config:option}`instance-snapshots:snapshots.pattern` and
{config:option}`instance-snapshots:snapshots.expiry`), or by the profile applied to the
instance. The replicator does not impose its own naming scheme.

If an instance already has a {config:option}`instance-snapshots:snapshots.schedule` set at
the instance or profile level, the replicator skips creating a new snapshot and reuses the
most recent existing snapshot as the reference point for the incremental copy instead.

When `snapshot` is not set (or set to `false`), no new snapshot is created before the
incremental copy runs. If existing snapshots are present on the instance, the copy operation
uses them to transfer only the delta; if no snapshots exist, the full instance is transferred.

```{note}
Snapshots created by replication accumulate over time. Use `snapshots.expiry` on the instance or
profile to automatically prune them, or delete them manually with `lxc snapshot delete`.
```

## Next steps

Once replicators are running, see {ref}`howto-replicators-manage` to view, configure, or delete replicators, and {ref}`howto-replicators-dr` to fail over to the standby cluster if the leader becomes unavailable.
