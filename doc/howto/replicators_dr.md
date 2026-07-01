---
myst:
  html_meta:
    description: How to perform disaster recovery failover and recovery using LXD replicators.
---

(howto-replicators-dr)=
# How to perform disaster recovery with replicators

Once you have {ref}`set up replicators <howto-replicators-setup>` for active-passive replication, you can use them to fail over to the standby cluster if the leader cluster becomes unavailable, and to restore the original replication direction when the leader comes back online.

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

After promoting the project on the standby cluster, it becomes writable. Start the instances to resume your workloads:

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
In the confirmation modal, check {guilabel}`Force stop` then click {guilabel}`stop`.
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
For a single-node cluster, click {guilabel}`Server` in the navigation sidebar, then select the {guilabel}`Replicators` tab in the main content pane. Otherwise, click {guilabel}`Clustering` in the navigation sidebar, then select {guilabel}`Replicators` from the expanded drop-down list.

Click on the "run" button at the end of the replicator's row.

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


## Related topics

How-to guides:

* {ref}`howto-replicators-setup`
* {ref}`howto-replicators-manage`
* {ref}`disaster-recovery-replication`
