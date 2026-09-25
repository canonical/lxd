---
myst:
  html_meta:
    description: How to set up replicators to sync instances across LXD cluster links for active-passive disaster recovery.
---

(howto-replicators-setup)=
# How to set up replicators

Replicators copy instances from a project on one cluster to a project on another cluster. You can prepare for active-passive disaster recovery by setting up replicators to periodically copy instances from a primary cluster to a secondary cluster. The primary cluster can handle workloads, and the secondary cluster can take over with its replica project if the primary cluster fails.

(howto-replicators-prereqs)=
## Prerequisites

Before setting up replicators:

- You must initialize two LXD clusters (the primary and secondary clusters).
- Network connectivity must exist between the clusters.
- You need sufficient permissions on both clusters to create cluster links and manage projects.
- You must create {ref}`bidirectional cluster links <howto-cluster-links-create-bidirectional>` on both clusters.

(howto-replicators-project-setup)=
## Create projects for replication

Set up projects with the same name on both clusters, and use the {config:option}`project-replica:replica.cluster` configuration key to specify the cluster link used for replication.

1. On the primary cluster, create a project with {config:option}`project-replica:replica.cluster` set to the cluster link that points to the secondary cluster:

   `````{tabs}
   ````{group-tab} CLI
   ```bash
   lxc project create <project_name> --config replica.cluster=<cluster_link_name>
   ```
   ````
   ````{group-tab} UI
   Expand the {guilabel}`Project` drop-down and select {guilabel}`+ Create project` at the bottom.

   Enter a name and optionally a description for the new project.

   Go to the new project's configuration and select the {guilabel}`Replication` tab.

   Under {guilabel}`Replica cluster`, select the cluster link that connects to the secondary cluster.
   ````
   `````

1. On the secondary cluster, create a project with the same name and use the {config:option}`project-replica:replica.cluster` configuration key to specify the cluster link that connects to the primary cluster:

   `````{tabs}
   ````{group-tab} CLI
   ```bash
   lxc project create <project_name> --config replica.cluster=<cluster_link_name>
   ```
   ````
   ````{group-tab} UI
   Expand the {guilabel}`Project` drop-down and select {guilabel}`+ Create project` at the bottom.

   Enter the same name as in the previous step, and optionally a description for the new project.

   Go to the new project's configuration and select the {guilabel}`Replication` tab.

   Under {guilabel}`Replica cluster`, select the cluster link that connects to the primary cluster.
   ````
   `````

(howto-replicators-auth)=
## Configure permissions

Replicators communicate over cluster links. On each cluster, you must configure the cluster link identity with the following {ref}`permissions <permissions>` on the project configured for replication on that cluster:

- `operator` on the project, so the cluster link can perform instance replication
- `can_edit` on the project, so the cluster link can validate and update replica project configuration as part of the workflow

For example, if the replicated project is called `myproject`, you can prepare an authentication group named `replicators` on each cluster:

```bash
lxc auth group create replicators
lxc auth group permission add replicators project myproject operator
lxc auth group permission add replicators project myproject can_edit
```

Then, on each cluster, add the cluster links to that authentication group, as described in {ref}`howto-cluster-links-permissions`.

(howto-replicators-create)=
## Create a replicator

After setting up projects and configuring cluster link permissions on both clusters, create a replicator on the primary cluster. The `cluster` configuration key is required and must be set to the name of an existing cluster link.

Each cluster link can be targeted by at most one replicator per project. Creating or updating a replicator to target a cluster link already used by another replicator in the same project fails with a conflict error.

`````{tabs}
````{group-tab} CLI

   ```bash
   lxc replicator create <replicator_name> cluster=<cluster_link_name> --project <project_name>
   ```

   For example:

   ```bash
   lxc replicator create my-replicator cluster=lxd-standby --project myproject
   ```

   You can also create a replicator with a schedule:

   ```bash
   lxc replicator create my-replicator cluster=lxd-standby schedule="@daily" --project myproject
   ```

````
````{group-tab} UI
   Click {guilabel}`Clustering` in the navigation sidebar, then select {guilabel}`Replicators` from the expanded drop-down list.

   Click on the {guilabel}`+ Create replicator` button to open the side panel.

   Select the cluster link established between the primary and secondary clusters.
   Enter a name and optionally a description for the new replicator.
   Select the {ref}`project that you configured <howto-replicators-project-setup>`.
   You can also enter a schedule.

   Click {guilabel}`Create`.

````
`````

See {ref}`ref-replicator-config` for all available configuration options.

(howto-replicators-replica-mode)=
## Configure project replica modes

After you have created a replicator on the primary cluster, you can demote the project on the secondary cluster to `standby` mode and promote the project on the primary cluster to `leader`.

1. On the secondary cluster, demote the project to `standby` mode:

   `````{tabs}
   ````{group-tab} CLI
   ```bash
   lxc project demote-replica <project_name>
   ```
   ````
   ````{group-tab} UI
   Go to the project configuration and select the {guilabel}`Replication` tab.

   Under {guilabel}`Replica mode`, click {guilabel}`Demote to standby`.
   ````
   `````

   Instances cannot be created or started in a project in `standby` mode; you must promote the project to `leader` during a failover before starting instances.

1. On the primary cluster, promote the project to `leader` mode:

   `````{tabs}
   ````{group-tab} CLI
   ```bash
   lxc project promote-replica <project_name>
   ```
   ````
   ````{group-tab} UI
   Go to the project configuration and select the {guilabel}`Replication` tab.

   Under {guilabel}`Replica mode`, click {guilabel}`Promote to leader`.
   ````
   `````

```{note}
You can only promote a project from an unset replica mode to `leader` mode if the project has at least one replicator.

Before allowing the promotion, LXD validates that all target projects on clusters referenced by the source project's replicators are in `standby` mode. (You can force promote a project to skip these checks.) This ensures that new instances are not created on a secondary cluster between replicator runs. If a target cluster is unreachable, promotion still proceeds to allow for disaster recovery scenarios in which the target is offline.
```

(howto-replicators-run)=
## Run a replicator

To manually trigger a replicator run:

`````{tabs}
````{group-tab} CLI

   Use the following command on the primary cluster:

   ```bash
   lxc replicator run <replicator_name>
   ```

````
````{group-tab} UI

   On the primary cluster, click {guilabel}`Clustering` in the navigation sidebar, then select {guilabel}`Replicators` from the expanded drop-down list.

   Click on the "run" button at the end of the replicator's row.

   Alternatively, click on a replicator name to view its detail page, then click on the {guilabel}`Run` button in the header.

````
`````

This syncs all instances in the source project, together with the custom volumes attached only to them, to the secondary cluster.

To schedule replication automatically, set the `schedule` configuration key with a cron expression:

`````{tabs}
````{group-tab} CLI

   ```bash
   lxc replicator set <replicator_name> schedule="0 0 * * *"
   ```

````
````{group-tab} UI

   In the {guilabel}`Create replicator` or {guilabel}`Edit replicator` side panel, enter a cron expression in the {guilabel}`Schedule` input box.

````
`````

(howto-replicators-snapshot)=
## Snapshot before replication

Each replicator run performs an incremental instance sync to the secondary cluster using
the equivalent of [`lxc copy --refresh`](lxc_copy.md). This transfers only the data that has changed since the last sync,
using any existing snapshots as a reference point to minimize the amount of data transferred.

Before the incremental copy, LXD creates a point-in-time snapshot of each source instance.
This gives the copy operation a consistent reference point, which reduces the amount of data
transferred on each sync and provides a rollback point on the source in case anything goes
wrong during replication.

Snapshot naming and expiry are controlled entirely by the instance's own configuration (for
example {config:option}`instance-snapshots:snapshots.pattern` and
{config:option}`instance-snapshots:snapshots.expiry`), or by the profile applied to the
instance. The replicator does not impose its own naming scheme.

If an instance already has a {config:option}`instance-snapshots:snapshots.schedule` set at
the instance or profile level, the replicator skips creating a new snapshot and reuses the
most recent existing snapshot as the reference point for the incremental copy instead.

```{note}
Snapshots created by replication accumulate over time. Use `snapshots.expiry` on the instance or
profile to automatically prune them, or delete them manually with `lxc snapshot delete`.
```

## Next steps

Once replicators are running, see {ref}`howto-replicators-manage` to view, configure, or delete replicators, and {ref}`howto-replicators-dr` to fail over to the secondary cluster if the primary cluster becomes unavailable.
