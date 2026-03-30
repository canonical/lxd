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

Both clusters need a project configured with the {config:option}`project-replica:replica.cluster` and {config:option}`project-replica:replica.mode` settings.

1. On the leader cluster, create a project and configure it to link to the standby cluster:

   ```bash
   lxc project create <project_name> -c replica.cluster=<standby_cluster_link_name>
   ```

1. On the standby cluster, create a project with the same name and configure it to link to the leader cluster:

   ```bash
   lxc project create <project_name> -c replica.cluster=<leader_cluster_link_name>
   ```

1. On the standby cluster, set the project's replica mode to `standby`. This prevents new instances from being created in the project directly; existing instances can still be managed until the project is promoted to `leader` during a failover.

   ```bash
   lxc project set <project_name> replica.mode=standby
   ```

1. On the leader cluster, set the project's replica mode to `leader`.

   ```bash
   lxc project set <project_name> replica.mode=leader
   ```

```{admonition} Setting the leader
:class: note

Setting `replica.mode=leader` on the leader cluster requires the target project on the standby cluster to already have `replica.mode=standby` set.
This ensures that new instances are not created on a standby cluster between replicator runs.
```

(howto-replicators-create)=
## Create a replicator

After configuring the projects on both clusters, create a replicator on the leader cluster. The `cluster` configuration key is required and must be set to the name of an existing cluster link.

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

When you set `snapshot=true` on a replicator, LXD creates a point-in-time snapshot of each source
instance before performing the incremental refresh to the standby cluster. This gives you a
consistent rollback point on the source in case anything goes wrong during replication.

Snapshot naming and expiry are controlled entirely by the instance's own configuration (for example
{config:option}`instance-snapshots:snapshots.pattern` and {config:option}`instance-snapshots:snapshots.expiry`), or by the profile applied to the instance. The
replicator does not impose its own naming scheme.

If an instance already has a {config:option}`instance-snapshots:snapshots.schedule` set at the instance or profile level, the
replicator skips creating a new snapshot and reuses the most recent existing snapshot for the
incremental refresh instead.

When `snapshot` is not set (or set to `false`), no new snapshot is created. If a previous snapshot
already exists on the instance, it is reused naturally by the incremental refresh.

```{note}
Snapshots created by replication accumulate over time. Use `snapshots.expiry` on the instance or
profile to automatically prune them, or delete them manually with `lxc snapshot delete`.
```

## Next steps

Once replicators are running, see {ref}`howto-replicators-manage` to view, configure, or delete replicators, and {ref}`howto-replicators-dr` to fail over to the standby cluster if the leader becomes unavailable.
