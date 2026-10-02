---
myst:
  html_meta:
    description: How to perform disaster recovery failover and failback through storage replication.
---

(disaster-recovery-replication)=
# How to perform disaster recovery with storage replication

Active-passive disaster recovery with storage replication requires advance preparation: you must set up storage replication between a primary and secondary LXD deployment. For details, see {ref}`howto-storage-replication-setup`.

If the primary deployment becomes unavailable, you can follow these steps to fail over to the secondary deployment, and then fail back when the primary deployment comes back online.

(disaster-recovery-replication-promote)=
## Promote the secondary location

If the primary location becomes unreachable, the secondary location can be promoted to become the new source of truth. The method to promote the secondary storage array depends on the storage vendor. For links to vendor guides, see: {ref}`disaster-recovery-replication-setup`.

```{important}
Promoting the secondary array might result in data loss if there is data on the primary location that has not been replicated. Consult the {ref}`storage vendor's documentation <disaster-recovery-replication-setup>` for further information.
```

(disaster-recovery-replication-recover)=
## Recover resources

After the secondary storage array has been promoted, you can start recovering the workload. Run the steps in {ref}`disaster-recovery` on the secondary LXD deployment.

When prompted to choose the pools to scan for unknown volumes, select the storage pool that was configured during the replication setup.

The instances and custom storage volumes are then recovered on the secondary LXD deployment. Use `lxc start` to bring up the instances that were originally running on the primary deployment.

(disaster-recovery-replication-add-pool)=
### Add missing storage pool

If the LXD storage pool at the secondary location exists only in the storage array and has not yet been created in LXD (as described in {ref}`disaster-recovery-replication-entities-pool`), you must recover it first.

Use the `lxc storage create` command to add the storage pool. This works for both single and clustered LXD deployments. For more information, see: {ref}`howto-storage-pools-create`.

(disaster-recovery-replication-add-pool-cephrbd)=
#### Recover Ceph RBD pool

LXD's {ref}`Ceph RBD driver <storage-ceph>` uses a _placeholder_ volume to reserve the storage pool and ensure it isn't used more than once. For replication, this behavior can be ignored because the replicated pool must be recovered at the secondary location. To allow this, set {config:option}`storage-ceph-pool-conf:source.recover` to ignore the placeholder volume if it was also replicated to the secondary location.

When creating the storage pool in a LXD cluster, make sure to add the `source.recover=true` setting when creating the pending storage pools per cluster member as this setting is cluster member specific.

(disaster-recovery-replication-failback)=
## Fail back to the primary location

Once the primary location is back online, the storage layer ensures data consistency because the secondary storage array now acts as the source of truth and no longer receives updates from the primary array. As long as this replication flow is not reversed, the running instances and custom volumes on the secondary location are protected.

```{warning}
Network collisions might occur if the primary location comes back online and LXD automatically starts up any instances. This issue is outside the scope of storage replication, but you must take appropriate measures to prevent such conflicts.
```

Service failback to the primary location can be performed in two ways. In both cases, the operations on the storage layer are identical, but the correct approach depends on the state of the instances and custom volumes on the secondary location:

1. Shut down the resources on the secondary location and bring them back up on the primary
   This approach requires that the configuration of the recovered instances and volumes on the secondary location has not been modified in any way. Any modifications would not be reflected in the database of the primary LXD deployment and might cause unexpected side effects.

1. Set up a fresh deployment of LXD on the primary location and repeat the steps outlined in {ref}`disaster-recovery`.
   This approach repeats the same process performed for the initial disaster recovery, but in reverse.

After choosing an approach, demote the storage array at the secondary location and promote the array at the primary location. Refer to {ref}`disaster-recovery-replication-setup` for details on how to perform these actions.

Finally, either bring up the instances on the primary deployment using `lxc start`, or recover them first to make them known again to the primary deployment before starting them.

## Related topics

How-to guides:

* {ref}`disaster-recovery`
* {ref}`cluster-recover`
* {ref}`storage`

Explanation:

* {ref}`exp-storage`

Reference:

* {ref}`storage-drivers`
