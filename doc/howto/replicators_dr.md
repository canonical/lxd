---
myst:
  html_meta:
    description: How to perform disaster recovery failover and recovery using LXD replicators.
---

(howto-replicators-dr)=
# How to perform disaster recovery with replicators

Once you have {ref}`set up replicators <howto-replicators-setup>` for active-passive replication, you can use them to fail over to the standby cluster if the leader cluster becomes unavailable, and to restore the original replication direction when the leader comes back online.

```{note}
If the project is {ref}`mirrored with Ceph RBD <howto-replicators-ceph>`, follow the steps in {ref}`howto-replicators-dr-ceph` instead.
```

## Failover process

If the leader cluster becomes unavailable, you can manually fail over to the standby cluster.

On the standby cluster, promote the replica project to become the leader:

`````{tabs}
````{group-tab} CLI
```bash
lxc project promote-replica <project_name>
```

If the leader cluster is unreachable, promotion proceeds automatically without requiring validation. Use `--force` to skip validation when the leader cluster is still reachable but you want to promote anyway (for example, during a planned takeover before demoting the leader):

```bash
lxc project promote-replica <project_name> --force
```
````
````{group-tab} UI
Select the project from the {guilabel}`Project` drop-down menu, then click {guilabel}`Configuration` in the navigation sidebar.

Select the {guilabel}`Replication` tab, then, under {guilabel}`Replica mode`, click {guilabel}`Promote to leader`.

If the leader cluster is unreachable, promotion proceeds automatically without requiring validation. Click {guilabel}`Promote` in the confirmation modal.

If the leader cluster is still reachable but you want to promote the replica project anyway (for example, during a planned takeover before demoting the leader), then check {guilabel}`Force` and click {guilabel}`Promote` to skip validation.
````
`````

After promoting the project on the standby cluster, the project becomes writable. Start the instances to resume your workloads:

`````{tabs}
````{group-tab} CLI
```bash
lxc start --all --project <project_name>
```
````
````{group-tab} UI
Select {guilabel}`Instances` in the navigation sidebar.
Click the checkbox in the header row to select all instances, then click {guilabel}`Start` in the page header.
In the confirmation modal, click {guilabel}`Start`.
````
`````

## Recovering the original leader cluster

When the original leader cluster comes back online, it will be out of sync with the new leader (the former standby). Scheduled replicator runs on the original leader cluster will fail because both projects are in leader mode.

A replicator run requires the source project to be in leader mode and the target project to be in standby mode.

To restore the original leader cluster and resume the original replication direction:

### 1. Sync from the new leader back to the original leader

On the original leader cluster, stop all running instances in the project before running restore.
The "restore" action is rejected if any local instance is running, to prevent partial restores.

`````{tabs}
````{group-tab} CLI
```bash
lxc stop <instance_name> [<instance_name>...] --force
```
````
````{group-tab} UI
Select {guilabel}`Instances` in the navigation sidebar.
Click the checkbox in the header row to select all instances, then click {guilabel}`Stop` in the page header.
In the confirmation modal, check {guilabel}`Force stop` then click {guilabel}`Stop`.
````
`````

Demote the project on the original leader cluster to standby mode:

`````{tabs}
````{group-tab} CLI
```bash
lxc project demote-replica <project_name>
```

If the new leader cluster is unreachable, use `--force` to skip the validation:
```bash
lxc project demote-replica <project_name> --force
```
````
````{group-tab} UI
Select the project from the {guilabel}`Project` drop-down menu, then click {guilabel}`Configuration` in the navigation sidebar.

Select the {guilabel}`Replication` tab, then, under {guilabel}`Replica mode`, click {guilabel}`Demote to standby`.

If the new leader is reachable, click {guilabel}`Demote`.
If the new leader is unreachable, check {guilabel}`Force` to skip the validation, then click {guilabel}`Demote`.
````
`````

On the original leader cluster, run the replicator in restore mode to pull data from the new leader:

`````{tabs}
````{group-tab} CLI
```bash
lxc replicator run <replicator_name> --restore
```
````
````{group-tab} UI
Click {guilabel}`Clustering` in the navigation sidebar, then select {guilabel}`Replicators` from the expanded drop-down list.

Click on the run button {{run_button}} at the end of the replicator's row.

Alternatively, click on a replicator name to view its detail page, then click on the {guilabel}`Restore` button in the header.

In the confirmation modal, check {guilabel}`Overwrite local data`, then click {guilabel}`Restore`.

````
`````

Restore mode uses the new leader's instance list as the authoritative source. Any instances created on the new leader during the failover period are also created on the recovering cluster automatically.

The original leader cluster is now a standby replica of the new leader cluster.

### 2. Resume original replication direction

To return to the original setup where the original leader cluster replicates to the standby, stop any running instances in the project on the new leader cluster (former standby). Next, demote the project on the new leader cluster back to standby mode:

`````{tabs}
````{group-tab} CLI
```bash
lxc project demote-replica <project_name>
```
````
````{group-tab} UI
Select the project from the {guilabel}`Project` drop-down menu, then click {guilabel}`Configuration` in the navigation sidebar.

Select the {guilabel}`Replication` tab, then, under {guilabel}`Replica mode`, click {guilabel}`Demote to standby`.
````
`````

Finally, promote the project on the original leader cluster back to leader mode:

`````{tabs}
````{group-tab} CLI
```bash
lxc project promote-replica <project_name>
```
````
````{group-tab} UI
Select the project from the {guilabel}`Project` drop-down menu, then click {guilabel}`Configuration` in the navigation sidebar.

Select the {guilabel}`Replication` tab, then, under {guilabel}`Replica mode`, click {guilabel}`Promote to leader`.
````
`````

Your original active-passive disaster recovery setup is now restored. You can restart your instances on the leader cluster and resume your scheduled replicator runs.

(howto-replicators-dr-ceph)=
## Disaster recovery with Ceph RBD mirroring

If the project is {ref}`mirrored with Ceph RBD <howto-replicators-ceph>`, LXD promotes and demotes the project's volumes in Ceph together with the project.
The steps differ from the ones above in these ways:

- If the leader cluster is unavailable, you must promote the standby project with `--force`.
- If the leader cluster is available, you must demote its project before you promote the standby project.
- You cannot run a replicator in restore mode.
  To recover the original leader cluster, you demote its project twice, which discards its volumes and copies them again from the new leader cluster, and then replicate from the new leader cluster.

(howto-replicators-dr-ceph-failover)=
### Fail over to the standby cluster

If the leader cluster becomes unavailable, promote the project on the standby cluster and start the instances:

```bash
lxc project promote-replica <project_name> --force
lxc start --all --project <project_name>
```

The `--force` flag is required because the leader cluster cannot demote its volumes while it is unavailable.
Without the flag, Ceph refuses the promotion and the command fails.

The instances start from the last mirror snapshot that reached the standby cluster.
Changes made on the leader cluster after the last successful replicator run are lost.

```{warning}
Use `--force` only if the leader cluster is unavailable.
A forced promotion makes the volumes on the two clusters diverge, and the volumes on the original leader cluster must then be discarded and copied again.
If both clusters are available, follow {ref}`howto-replicators-dr-ceph-switchover` instead.
```

(howto-replicators-dr-ceph-return)=
### Recover the original leader cluster

When the original leader cluster comes back online, its project is still in leader mode, and its volumes have diverged from the volumes on the new leader cluster.
LXD might also have restarted the instances that were running when the cluster became unavailable.

1. On the original leader cluster, stop all instances in the project:

   ```bash
   lxc stop --all --force --project <project_name>
   ```

1. On the original leader cluster, allow the new leader cluster to replicate to the project, and demote the project:

   ```bash
   lxc project set <project_name> replica.cluster=<new_leader_cluster_link_name>
   lxc project demote-replica <project_name>
   ```

   This makes the project's volumes on this cluster read-only.

1. Wait until Ceph reports that the volumes have diverged from the volumes on the new leader cluster.
   To check, run the following command against the original leader's Ceph cluster:

   ```bash
   rbd mirror pool status <osd_pool_name> --verbose
   ```

   Within about a minute of the demotion, the project's volumes show the state `up+error` with the description `split-brain`.

1. On the original leader cluster, demote the project again:

   ```bash
   lxc project demote-replica <project_name>
   ```

   LXD discards every volume of the project that Ceph reports as `split-brain`, and Ceph copies it again in full from the new leader cluster.
   Changes that did not reach the standby cluster before the failover are lost.

1. Wait until Ceph has copied the volumes.
   To check the progress, run the `rbd mirror pool status` command again.
   The project's volumes are ready when their state is `up+replaying`.

1. On the new leader cluster, create a replicator that targets the original leader cluster, and run it:

   ```bash
   lxc replicator create <replicator_name> cluster=<original_leader_cluster_link_name> --project <project_name>
   lxc replicator run <replicator_name> --project <project_name>
   ```

   The instances on the new leader cluster can keep running.
   If the run fails because Ceph has not finished copying the volumes, wait and run the replicator again.
   If the run fails because it reports a volume as `split-brain`, demote the project on the original leader cluster again.

The original leader cluster is now a standby replica of the new leader cluster.
To move the instances back to it, follow {ref}`howto-replicators-dr-ceph-switchover`.

```{note}
If you deleted an instance or a custom volume on the new leader cluster while the original leader cluster was unavailable, Ceph removes its volume from the original leader cluster when it copies the volumes again, but the record remains there.
Delete the record on the original leader cluster yourself.
```

(howto-replicators-dr-ceph-switchover)=
### Switch the leader cluster while both clusters are available

Use these steps for a planned takeover, or to return to the original leader cluster after you have recovered it.
No data is lost.

1. On the leader cluster, stop all instances in the project:

   ```bash
   lxc stop --all --project <project_name>
   ```

1. On the leader cluster, run the replicator one more time, so that the final state reaches the standby cluster:

   ```bash
   lxc replicator run <replicator_name> --project <project_name>
   ```

1. On the leader cluster, make sure that {config:option}`project-replica:replica.cluster` is set to the cluster link of the other cluster, and demote the project:

   ```bash
   lxc project set <project_name> replica.cluster=<standby_cluster_link_name>
   lxc project demote-replica <project_name>
   ```

   The command fails if any instance in the project is still running.

1. On the standby cluster, promote the project without `--force` and start the instances:

   ```bash
   lxc project promote-replica <project_name>
   lxc start --all --project <project_name>
   ```

   If the promotion fails because the demotion has not reached this cluster yet, wait a moment and try again.

To replicate in the new direction, create a replicator on the new leader cluster that targets the other cluster, or run the one that already exists there.


## Related topics

How-to guides:

* {ref}`howto-replicators-setup`
* {ref}`howto-replicators-manage`
* {ref}`disaster-recovery-replication`
