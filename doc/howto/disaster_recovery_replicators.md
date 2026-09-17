---
myst:
  html_meta:
    description: How to perform disaster recovery failover and failback between clusters using LXD replicators.
---

(howto-replicators-dr)=
# How to perform disaster recovery with replicators

Active-passive disaster recovery with replicators requires advance preparation: you must set up replicators on the primary cluster to regularly copy instances to a secondary cluster. For details, see {ref}`howto-replicators-setup`.

If the primary cluster becomes unavailable, you can promote your secondary cluster to take over workloads. Then, after the primary cluster comes back online, you can synchronize the clusters and resume the original replication setup.

```{important}
Changing replica modes does not redirect application traffic. You must manage traffic separately from LXD.
```

## Fail over to the secondary cluster

If the primary cluster becomes unavailable, you can manually fail over to the secondary cluster.

On the secondary cluster, promote the replica project to `leader` mode:

`````{tabs}
````{group-tab} CLI
```bash
lxc project promote-replica <project_name>
```

If the primary cluster is unreachable, promotion proceeds automatically without requiring validation. Use `--force` only when the primary cluster is still reachable and its project is still in `leader` mode (this leaves both clusters writable):

```bash
lxc project promote-replica <project_name> --force
```

For a planned takeover (rather than a failover), do not use `--force`; instead, demote the source project first, then promote the replica project on the secondary cluster.

````
````{group-tab} UI
Select the project from the {guilabel}`Project` drop-down menu, then click {guilabel}`Configuration` in the navigation sidebar.

Select the {guilabel}`Replication` tab, then, under {guilabel}`Replica mode`, click {guilabel}`Promote to leader`.

If the primary cluster is unreachable, promotion proceeds automatically without requiring validation. Click {guilabel}`Promote` in the confirmation modal.

If the primary cluster is still reachable and its project remains in `leader` mode, checking {guilabel}`Force` before clicking {guilabel}`Promote` skips validation, but leaves both clusters writable. For a planned takeover, demote the project on the primary cluster first, then promote the project on the secondary cluster without checking {guilabel}`Force`.
````
`````

Once you promote the project on the secondary cluster to `leader` mode, the project becomes writable. Start the instances to resume your workloads:

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

You can then verify that the instances start successfully.

`````{tabs}
````{group-tab} CLI
Run this command to confirm that all instances have state `RUNNING`:

```bash
lxc list
```

You can also run the following command to check that custom volumes have been mounted:

```bash
lxc exec <instance_name> -- df -h
```
````
````{group-tab} UI
On the {guilabel}`Instances` page, verify that the status for each instance changes from {guilabel}`Stopped` to {guilabel}`Running`.
````
`````

Instances may fail to boot for application-related reasons, or may require a strict boot order, which LXD does not orchestrate automatically. If a virtual machine fails to boot, you can {ref}`attach its root volume to another virtual machine <storage-volumes-attach-vm>` to investigate its contents.

Refer to the {ref}`troubleshooting guides <troubleshoot>` if you encounter any issues.

```{note}
Verifying that an instance is `RUNNING` does not confirm application readiness. You must manage application validation separately from LXD.
```

## Fail back to the primary cluster

After failover, the secondary cluster manages workloads in `leader` mode. As a result, when the primary cluster comes back online, the original source project will be out of sync with the replica project on the secondary cluster. Scheduled replicator runs on the primary cluster will also fail because both projects are in `leader` mode. To fail back to the primary cluster, you must synchronize the projects, restore the original replica modes, and resume the original replication direction.

### Verify cluster restoration

Once the primary cluster comes back online, you can confirm the health of the cluster.

`````{tabs}
````{group-tab} CLI
Run this command to ensure that all cluster members are back online:

```bash
lxc cluster list
```
````
````{group-tab} UI
Click {guilabel}`Clustering` in the navigation sidebar, select {guilabel}`Members` from the expanded drop-down list, and confirm that all members have status {guilabel}`Online`.
````
`````

### Synchronize the projects

You can run the replicator on the primary cluster in "restore" mode to synchronize the original source project with the replica project on the secondary cluster. The replicator only runs in "restore" mode if all instances in the source project are stopped. This prevents partial restoration of instances. To synchronize the primary and secondary clusters, first stop all running instances in the source project on the primary cluster:

`````{tabs}
````{group-tab} CLI
Run this command to stop running instances:

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

Then demote the source project on the primary cluster to `standby` mode:

`````{tabs}
````{group-tab} CLI
```bash
lxc project demote-replica <project_name>
```

You can only demote a project if {config:option}`project-replica:replica.cluster` is set. Use `--force` to demote a project that does not have this key configured:

```bash
lxc project demote-replica <project_name> --force
```
````
````{group-tab} UI
Select the project from the {guilabel}`Project` drop-down menu, then click {guilabel}`Configuration` in the navigation sidebar.

Select the {guilabel}`Replication` tab, then, under {guilabel}`Replica mode`, click {guilabel}`Demote to standby`.

You can only demote a project if {config:option}`project-replica:replica.cluster` is set. Check {guilabel}`Force` before clicking {guilabel}`Demote` to demote a project that does not have this key configured.
````
`````

On the primary cluster, run the replicator in "restore" mode to copy instances from the secondary cluster back to the primary cluster:

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

Restore mode uses the instance list from the secondary cluster as the authoritative source. Any instances created on the secondary cluster during the failover period are also created on the primary cluster automatically.

The project on the primary cluster is now a standby replica of the project on the secondary cluster, though the replicator remains on the primary cluster.

### Resume replication direction

To restore the original setup, in which the primary cluster manages workloads and the replicator copies instances from the primary to the secondary cluster, stop any running instances in the project on the secondary cluster. Next, demote the project on the secondary cluster back to `standby` mode:

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

Finally, promote the project on the primary cluster back to `leader` mode. During promotion, LXD identifies its associated replica project from the {config:option}`project-replica:replica.cluster` key and confirms that the target project on the secondary cluster is in `standby` mode. If this key is not set, set it to the cluster link that points to the secondary cluster before you promote the project.

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

Your original active-passive disaster recovery setup is now restored. You can restart your instances on the primary cluster and resume your scheduled replicator runs.

## Related topics

How-to guides:

- {ref}`howto-replicators-setup`
- {ref}`howto-replicators-manage`
- {ref}`disaster-recovery-replication`

Explanation:

- {ref}`exp-replicators`
