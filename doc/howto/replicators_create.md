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

To let Ceph RBD mirroring copy the data instead of LXD, your setup must also meet the {ref}`prerequisites for Ceph RBD mirroring <howto-replicators-ceph-prereqs>`.

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

   `````{tabs}
   ````{group-tab} CLI
   ```bash
   lxc project create <project_name>
   ```
   ````
   ````{group-tab} UI
   Expand the {guilabel}`Project` drop-down and select {guilabel}`+ Create project` at the bottom.

   Enter a name and optionally a description for the new project.
   ````
   `````

1. On the standby cluster, create a project with the same name and configure it to accept replication from the leader cluster:

   `````{tabs}
   ````{group-tab} CLI
   ```bash
   lxc project create <project_name> -c replica.cluster=<leader_cluster_link_name>
   ```
   ````
   ````{group-tab} UI
   Expand the {guilabel}`Project` drop-down and select {guilabel}`+ Create project` at the bottom.

   Enter the same name as in the previous step, and optionally a description for the new project.

   Go to the new project's configuration and select the {guilabel}`Replication` tab.

   Under {guilabel}`Replica cluster`, select the cluster link established between the standby and the leader.
   ````
   `````

1. On the standby cluster, demote the project to standby mode. This prevents new instances from being created in the project and existing instances from being started. The project must be promoted to `leader` during a failover before instances can be started.

   `````{tabs}
   ````{group-tab} CLI
   ```bash
   lxc project demote-replica <project_name>
   ```
   ````
   ````{group-tab} UI
   Under {guilabel}`Replica mode`, click {guilabel}`Demote to standby`.
   ````
   `````

1. On the leader cluster, promote the project to leader mode:

   `````{tabs}
   ````{group-tab} CLI
   ```bash
   lxc project promote-replica <project_name>
   ```
   ````
   ````{group-tab} UI
   Go to the leader project's configuration and select the {guilabel}`Replication` tab.

   Under {guilabel}`Replica mode`, click {guilabel}`Promote to leader`.
   ````
   `````

```{admonition} Promote validation
:class: note

The `lxc project promote-replica` command validates that all target projects (on clusters referenced by the project's replicators) are in standby mode before allowing the promotion.
This ensures that new instances are not created on a standby cluster between replicator runs.
If a target cluster is unreachable, promotion still proceeds to allow disaster recovery scenarios where the target may be offline.
```

(howto-replicators-ceph)=
## Replicate the data with Ceph RBD mirroring

By default, a replicator copies the data of every instance over the cluster link.
If both clusters keep the project on {ref}`Ceph RBD <storage-ceph>` storage pools, you can let Ceph copy the data through RBD mirroring.
LXD then sends only the instance and volume records over the cluster link.
See {ref}`exp-replicators-ceph` for details.

Skip this section if you do not want to use Ceph RBD mirroring.

(howto-replicators-ceph-prereqs)=
### Prerequisites for Ceph RBD mirroring

Make sure that your setup meets these prerequisites:

- Both clusters support the {ref}`extension-storage-ceph-replicator` API extension.
- Each cluster uses its own Ceph cluster.
- The LXD storage pool has the same name on both clusters, and so does the OSD pool behind it.
  Ceph mirrors a volume to the OSD pool of the same name, and the instance records refer to the storage pool by name.
- The standby cluster's storage pool is a regular storage pool.
  Do not create it with {config:option}`storage-ceph-pool-conf:source.recover`, which is meant for {ref}`storage replication <disaster-recovery-replication-add-pool-cephrbd>` of a whole OSD pool.
- RBD mirroring is enabled in `image` mode on the OSD pool in both Ceph clusters.
  In `pool` mode, Ceph would mirror every volume in the OSD pool, including the volumes of other projects.
- The two Ceph clusters are peers of each other in both directions (`rx-tx`), and the `rbd-mirror` daemon runs in both.
  After a failover, the clusters swap roles, so each Ceph cluster must be able to receive data from the other.
- The profiles that the project's instances use exist on the standby cluster with the same devices, including a root disk device on the mirrored storage pool.

See the [RBD mirroring](https://docs.ceph.com/en/reef/rbd/rbd-mirroring/) documentation for how to enable mirroring and add the peers.

(howto-replicators-ceph-pools)=
### Configure the storage pools

On each cluster, the {config:option}`ceph.replicator <storage-ceph-pool-conf:ceph.replicator.<project>>` key on the storage pool names the peer Ceph site, which is the Ceph cluster that the other cluster uses.
To see the site names, run the following command against either Ceph cluster:

```bash
rbd mirror pool info <osd_pool_name>
```

1. On the leader cluster, set the key to the site name of the standby's Ceph cluster:

   ```bash
   lxc storage set <pool_name> ceph.rbd.clone_copy=false ceph.replicator.<project_name>=<standby_ceph_site_name>
   ```

1. On the standby cluster, set the key to the site name of the leader's Ceph cluster:

   ```bash
   lxc storage set <pool_name> ceph.rbd.clone_copy=false ceph.replicator.<project_name>=<leader_ceph_site_name>
   ```

The project must exist before you can set the key.

```{important}
Set {config:option}`storage-ceph-pool-conf:ceph.rbd.clone_copy` to `false` before you create any container on the storage pool.
Ceph cannot mirror a volume that is a clone of its image.
This setting applies to all projects that use the storage pool.
```

While the key is set, the following rules apply to the project:

- Every instance, and every custom volume attached only to one instance, must be on a storage pool that has the key.
  Otherwise, the replicator refuses to run.
- Ceph mirrors all instance volumes and custom volumes that the project holds on the storage pool.
  The standby cluster, however, only receives records for instances and for custom volumes attached only to one instance.
  Do not attach a custom volume to more than one instance in a mirrored project: unlike with other replicators, you cannot create it in advance on the standby cluster.
- You cannot rename the project.
- To delete the project, unset the key first, or delete the project with `--force`, which also removes the key.

(howto-replicators-create)=
## Create a replicator

After configuring the projects on both clusters, create a replicator on the leader cluster. The `cluster` configuration key is required and must be set to the name of an existing cluster link.

Each cluster link can be targeted by at most one replicator per project. Creating or updating a replicator to target a cluster link already used by another replicator in the same project fails with a conflict error.

`````{tabs}
````{group-tab} CLI

   ```bash
   lxc replicator create <replicator_name> cluster=<standby_cluster_link_name> --project <project_name>
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

   Select the cluster link established between the leader and the standby.
   Enter a name and optionally a description for the new replicator.
   Select the {ref}`project that you configured <howto-replicators-project-setup>`.
   You can also enter a schedule.

   Click {guilabel}`Create`.

````
`````

See {ref}`ref-replicator-config` for all available configuration options.

(howto-replicators-run)=
## Run a replicator

To manually trigger a replicator run:

`````{tabs}
````{group-tab} CLI

   Use the following command on the leader cluster:

   ```bash
   lxc replicator run <replicator_name>
   ```

````
````{group-tab} UI

   On the leader cluster, click {guilabel}`Clustering` in the navigation sidebar, then select {guilabel}`Replicators` from the expanded drop-down list.

   Click on the "run" button at the end of the replicator's row.

   Alternatively, click on a replicator name to view its detail page, then click on the {guilabel}`Run` button in the header.

````
`````

This syncs all instances in the source project, together with the custom volumes attached only to them, to the standby cluster.

If the project is {ref}`mirrored with Ceph RBD <howto-replicators-ceph>`, the run completes only after the standby's Ceph cluster has received the data.
The first run copies every volume in full, so it can take a long time.
If Ceph needs more than one hour, the run fails, but Ceph continues to copy the data.
In this case, run the replicator again later.

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

Each replicator run performs an incremental instance sync to the standby cluster using
the equivalent of `lxc copy --refresh`. This transfers only the data that has changed since the last sync,
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

Once replicators are running, see {ref}`howto-replicators-manage` to view, configure, or delete replicators, and {ref}`howto-replicators-dr` to fail over to the standby cluster if the leader becomes unavailable.
