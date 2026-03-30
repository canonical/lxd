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

```bash
lxc project set <project_name> replica.mode=leader
```

After this command, the project on the standby cluster becomes writable. Start the instances to resume your workloads:

```bash
lxc start --all --project <project_name>
```

## Recovering the original leader cluster

When the original leader cluster comes back online, it will be out of sync with the new leader (the former standby). Scheduled replicator runs on the original leader cluster will fail because both projects have `replica.mode=leader`.

A replicator run requires the source project to have `replica.mode=leader` and the target project to have `replica.mode=standby`.

To restore the original leader cluster and resume the original replication direction:

### 1. Sync from the new leader back to the original leader

On the original leader cluster, stop all running instances in the project before running restore.
The `--restore` action is rejected if any local instance is running, to prevent partial restores:

```bash
lxc stop <instance_name> [<instance_name>...] --force
```

Set the project on the original leader cluster to standby mode:

```bash
lxc project set <project_name> replica.mode=standby
```

On the original leader cluster, run the replicator in restore mode to pull data from the new leader:

```bash
lxc replicator run <replicator_name> --restore
```

Restore mode uses the new leader's instance list as the authoritative source. Any instances created on the new leader during the failover period are also created on the recovering cluster automatically.

The original leader cluster is now a standby replica of the new leader cluster.

### 2. Resume original replication direction

To return to the original setup where the original leader cluster replicates to the standby, stop any running instances in the project for the new leader cluster (former standby). Next, set the project on the new leader cluster back to standby mode:

```bash
lxc project set <project_name> replica.mode=standby
```

Finally, set the project on the original leader cluster back to leader mode:

```bash
lxc project set <project_name> replica.mode=leader
```

Your original active-passive disaster recovery setup is now restored. You can restart your instances on the leader cluster and resume your scheduled replicator runs.


## Related topics

How-to guides:

* {ref}`howto-replicators-setup`
* {ref}`howto-replicators-manage`
* {ref}`disaster-recovery-replication`
