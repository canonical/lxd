---
myst:
  html_meta:
    description: How to prepare for and perform cross-site, active-passive disaster recovery with LXD, using replicators to manage instance replication, failover, and failback between clusters.
---

(howto-disaster-recovery)=
# How to perform disaster recovery

This how-to page guides you through the steps to prepare for and perform cross-site, active-passive disaster recovery with LXD.

Active-passive disaster recovery requires two LXD clusters: a primary cluster on one site that manages workloads and a secondary cluster on another site that can take over in the event of a disaster.
You can set up replicators to manage data replication from the primary cluster to the secondary cluster, and to manage failover and failback during and after a disaster.

For additional information, refer to the detailed {ref}`replicators explanation <exp-replicators>`.

```{note}
LXD also supports additional disaster recovery methods:

- In place of replicators, data replication at the storage array level is possible when using remote storage drivers that support volume recovery.
  Refer to the {ref}`storage replication how-to guide <disaster-recovery-replication>` for details about setup, failover, and failback.
- The `lxd recover` tool can be used to recover database records in the event that the LXD database is corrupted or lost.
  See the {ref}`recovery tool how-to guide <disaster-recovery>` for details.

You can also get support for a [MicroCloud](https://canonical.com/microcloud/docs/latest/) with an [Ubuntu Pro subscription](https://ubuntu.com/pricing/pro).
Visit the [Ubuntu Pro documentation](https://ubuntu.com/pro/docs/) for additional information.
```

(howto-disaster-recovery-prereqs)=
## Disaster recovery prerequisites

To prepare for a disaster, you must set up:

1. Replicators, to facilitate data replication between clusters
2. Observability systems, to receive alerts about potential failure conditions and to assess your clusters
3. A decision matrix, to establish conditions under which to authorize failover

### Set up replicators

Follow the steps in {ref}`howto-replicators-create` and {ref}`howto-replicators-manage` to configure your clusters for replication.

Note that, after you create a replicator on the primary cluster, you should configure the {ref}`replica mode <exp-replicators-concepts>` of projects on the clusters as follows:

- Promote the project on the primary cluster to `leader` replica mode
- Demote the project on the secondary cluster to `standby` replica mode

You can then {ref}`schedule replication to run automatically <howto-replicators-run>` based on your Recovery Point Objective (RPO; acceptable amount of data loss).

```{note}
You must set up the same network definitions and storage pool configurations on both clusters to set up the replicator.
You can use a {ref}`preseed file <preseed-yaml-file-fields>` to {ref}`initialize LXD <initialize-preseed>` for consistent configuration.
You can also facilitate alignment between clusters by following Infrastructure as Code practices, such as version control and a declarative approach to configuration.
```

### Set up observability systems

Observability systems make it possible for you to receive alerts about potential disasters, and to assess conditions on your clusters.

{ref}`Set up Prometheus <metrics-prometheus>` to monitor metrics collected by LXD, and visit the [Prometheus documentation](https://prometheus.io/docs/alerting/latest/configuration/) for information about how to configure alerts when those metrics meet certain conditions.
For example, you can configure Prometheus to issue alerts when:

- Replicator status transitions to `Failed`
- Cluster health degrades
- Site endpoints become unreachable

LXD also publishes information about its activity in the form of events.
You can {ref}`configure LXD to send these events to Loki <logs_loki>`, to preserve a log that you can use to assess failures.

Refer to the {ref}`metrics reference <provided-metrics>` and {ref}`events reference <events>` for details about data that you can collect from LXD.
For additional information about observability, visit the [Canonical Observability Stack (COS) documentation](https://documentation.ubuntu.com/observability/track-3.0/).

(howto-disaster-recovery-decision-matrix)=
### Set up a decision matrix for authorizing failover

You should not initiate a failover to the secondary cluster for transient network drops.
Prepare a decision matrix based on your RPO and Recovery Time Objective (RTO; acceptable amount of downtime) to establish the conditions under which failover is authorized.
For example, you might decide to authorize failover if the primary cluster has been unreachable for a specific amount of time.

(howto-disaster-recovery-discover)=
## Discover and verify a disaster

If you receive an alert from observability systems about a potential failure on your primary cluster, assess the situation before initiating failover to the secondary cluster.
For example, you should differentiate between network partitions, individual instance errors, and full-site outages.
In some cases, you can evaluate these conditions with LXD, but in other cases you must perform external infrastructure checks using separate Out-of-Band Management (OOBM) interfaces or data center power monitoring tools.

Example scenarios include:

- An unreachable cluster member, I/O timeout, or replicator failure may indicate a network partition or drop.
  Under these circumstances, you must check the underlying infrastructure (for example, routers and firewalls) to determine whether this is a transient issue or a hard network split.
- An instance boot failure may indicate an OS- or application-level issue inside an instance.
  You must perform an external infrastructure check to evaluate these errors.
- In the event of a full site outage, you will not be able to access LXD or Loki logs.
  You must detect this scenario through external monitoring.

You can follow our {ref}`troubleshooting guides <troubleshoot>` to try to troubleshoot and debug LXD, including {ref}`instance errors <instances-troubleshoot>` or {ref}`network issues <network-ipam>`.
You can also refer to the {ref}`events reference <events>` for details about the kinds of LXD actions that you can aggregate in logs.

Once you have verified an outage or other failure, refer to your {ref}`decision matrix <howto-disaster-recovery-decision-matrix>` to determine whether failover is authorized.

(howto-disaster-recovery-failover)=
## Fail over to the secondary cluster

Once you have authorized failover, you can follow these steps to enable your secondary cluster to take over workloads.

### Demote the source project

If the primary cluster is still reachable, demote the source project to `standby` mode:

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

If the primary cluster is unreachable, you can still promote the replica project in the next step.

### Promote the replica project

On the secondary cluster, promote the replica project to `leader` mode:

`````{tabs}
````{group-tab} CLI
```bash
lxc project promote-replica <project_name>
```

If the primary cluster is unreachable, promotion proceeds automatically without requiring validation.
Use `--force` to skip validation when the primary cluster is still reachable but you want to promote the replica project anyway (for example, during a planned takeover before demoting the leader):

```bash
lxc project promote-replica <project_name> --force
```
````
````{group-tab} UI
Select the project from the {guilabel}`Project` drop-down menu, then click {guilabel}`Configuration` in the navigation sidebar.

Select the {guilabel}`Replication` tab, then, under {guilabel}`Replica mode`, click {guilabel}`Promote to leader`.

If the secondary cluster is unreachable, promotion proceeds automatically without requiring validation.
Click {guilabel}`Promote` in the confirmation modal.

If the primary cluster is still reachable but you want to promote the replica project anyway (for example, during a planned takeover before demoting the leader), then check {guilabel}`Force` and click {guilabel}`Promote` to skip validation.
````
`````

### Route traffic to the secondary cluster

After you have promoted the replica project to `leader`, manually update DNS records to point client traffic to the secondary cluster's ingress IP addresses.

### Start instances on the secondary cluster

Once the replica project is promoted to `leader`, it becomes writable.
Start the instances to resume your workloads:

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

Be sure to verify that the instances start successfully, along with custom volumes.

`````{tabs}
````{group-tab} CLI
Run this command to confirm that all instances have state `RUNNING`:

```bash
lxc list
```

Run this command to check that custom volumes have been successfully mounted and are readable inside the guest OS:

```bash
lxc exec <instance_name> -- df -h
```
````
````{group-tab} UI
On the {guilabel}`Instances` page, verify that the status for each instance changes from {guilabel}`Stopped` to {guilabel}`Running`.
````
`````

Refer to our {ref}`troubleshooting guides <troubleshoot>` if you encounter any issues.
Note that, in the event that you encounter a failing root volume, you can {ref}`attach it to an instance <storage-attach-volume>` to investigate its contents.

```{note}
Instances may fail to boot for application-related reasons, or may require a strict boot order, which LXD does not orchestrate automatically.
```

### Verify application endpoints

You can facilitate verification of applications after failover by ensuring in advance that they expose a `/health` endpoint.

(howto-disaster-recovery-failback)=
## Resync data and fail back

Once your primary cluster comes back online, you can follow these steps to reverse replication and revert the clusters back to their original roles.

### Verify cluster restoration

Use your infrastructure monitoring tools to verify the underlying hardware and network stability of the primary cluster.
Then, confirm the health of the cluster.

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

### Resync the clusters

When the primary cluster comes back online, it will be out of sync with the secondary cluster, which has been managing workloads as `leader`.
Even if you were unable to demote the source project on the primary cluster to `standby` mode, scheduled replicator runs on the primary cluster will now fail because both projects are in `leader` mode.

To sync the primary cluster with the secondary cluster, stop all running instances in the source project on the primary cluster.

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

Demote the project on the primary cluster to `standby` mode:

`````{tabs}
````{group-tab} CLI
```bash
lxc project demote-replica <project_name>
```
````
````{group-tab} UI
Select the project from the {guilabel}`Project` drop-down menu, then click {guilabel}`Configuration` in the navigation sidebar.

Select the {guilabel}`Replication` tab, then, under {guilabel}`Replica mode`, click {guilabel}`Demote to standby`.
Then click {guilabel}`Demote` in the confirmation modal.
````
`````

Run the replicator in "restore" mode to copy instances from the secondary cluster back to the primary cluster.

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

```{note}
A replicator will not run in "restore" mode if any instance is running in the source project on the primary cluster.
This prevents partial restoration of instances.
```

Restore mode uses the instance list from the secondary cluster as the authoritative source.
Any instances created on the secondary cluster during the failover period are also created on the primary cluster automatically.

The primary cluster is now a standby replica of the secondary cluster, though the replicator remains on the primary cluster.

### Reverse cluster roles

To return to the original setup under which the replicator copies instances from the primary to the secondary cluster, stop any running instances in the project on the secondary cluster.
Next, demote the project on the secondary cluster back to `standby` mode:

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

Finally, promote the project on the primary cluster back to `leader` mode:

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

Your original active-passive disaster recovery setup is now restored.

Restart instances on the primary cluster, verify that the instances boot, and resume your scheduled replicator runs.

### Route traffic back to the primary cluster

Once the primary cluster has resumed its role as `leader`, manually update DNS records to point client traffic to the primary cluster's ingress IP addresses.
