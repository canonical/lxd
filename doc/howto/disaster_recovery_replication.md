(disaster-recovery-replication)=
# How to perform disaster recovery with storage replication

To enable disaster recovery, set up a secondary LXD deployment in a different location that can take over running workloads if a non-clustered LXD server or an entire cluster goes offline or becomes unreachable.

If such an incident occurs, you can rely on the storage layer that replicates all instances and custom volumes to the secondary location. You can then consolidate the storage layer and recover the resources to make them available to
your secondary deployment (see {ref}`disaster-recovery`).

This requires not only two separate LXD deployments, but also storage replication configuration for the respective storage array.

```{admonition} When this applies
:class: note
Recovery with storage replication is only possible when using remote {ref}`storage-drivers` which support volume recovery (see {ref}`storage-drivers-features`). Configuring replication on the storage array is out of scope for LXD and highly dependent on
how each vendor implements replication.

This how-to guide focuses on the steps performed within LXD and mentions storage array requirements where applicable.
```

In this guide, we assume two LXD deployments: a primary and a secondary. Each deployment is configured to use only its own co-located storage array, and both operate independently.

(disaster-recovery-replication-entities)=
## Set up entities at each location

Before you can set up storage replication, you must set up the required {ref}`entities <explanation-entities>` at each location.

(disaster-recovery-replication-entities-pool)=
### Storage pool

Ensure that both the primary and secondary LXD deployments have a storage pool on their respective storage arrays that can later be used for replication.

If you need to create a storage pool at either location, see: {ref}`storage-create-pool`.

(disaster-recovery-replication-entities-other)=
### Networks and profiles

You might also want to set up other entities, such as {ref}`networks <networks>` and {ref}`profiles <profiles>`, in advance on the secondary location. This way, in the event of a disaster, you can focus on recovering the volumes.

When performing {ref}`disaster-recovery`, LXD checks if the required entities are present and notifies you if anything is missing. The recovery does not create these entities.

(disaster-recovery-replication-setup)=
## Set up storage replication

Replication must be configured outside of LXD, according to the concepts and constructs by the storage vendor.

The following links lead to replication setup guides published by various storage vendors:

* Ceph RBD: [RBD mirroring](https://docs.ceph.com/en/reef/rbd/rbd-mirroring/)
* Dell PowerFlex: [Introduction to Replication](https://infohub.delltechnologies.com/en-us/t/dell-powerflex-introduction-to-replication/)

Once you have configured the connection between the primary and secondary storage arrays, follow the relevant storage vendor's steps to set up the actual replication of volumes.

Some vendors (such as Dell) use a concept called replication consistency group (RCG), which allows consistent replication of a group of volumes. An RCG can contain an instance's volume along with all of its attached custom volumes. Other vendors might use different concepts.

(disaster-recovery-replication-limitations)=
### Known storage array limitations

When setting up replication, consider the following limitations:

(disaster-recovery-replication-limitations-powerflex)=
#### PowerFlex

Cannot replicate and recover volumes with snapshots
: In {ref}`PowerFlex <storage-powerflex>`, a volume's snapshot appears as its own volume but is still logically connected to its parent volume (vTree).
  When replicating a volume inside a RCG, its snapshots are not replicated; this causes inconsistencies on the secondary location.
  A volume's snapshot can be replicated but will be placed inside a new vTree, losing the logical relation to its parent volume.
  During recovery, LXD notices this inconsistency and raises an error.

(disaster-recovery-replication-cephrdb)=
#### Ceph RBD

Cannot use journaling mode
: On {ref}`Ceph RBD <storage-ceph>` storage arrays, it's possible to configure mirroring using either journaling or snapshot mode.
  However, with LXD, only snapshot mode is supported. This is because the volumes need to be mapped to the host for read access during recovery, which might not be possible due to missing kernel features.

(disaster-recovery-replication-verify)=
## Verify replication

After setting up storage replication, confirm that the primary location's volumes are successfully replicated to the secondary location.

```{admonition} Check replication regularly
:class: important

For recovery, it's essential that replication is running consistently, so be sure to check this regularly. If the replication fails to run, you are at risk of losing data whenever the primary location experiences an outage.
```

(disaster-recovery-replication-promote)=
## Promote secondary location after disaster

If the primary location becomes unreachable, the secondary location can be promoted to become the new source of truth. The method to promote the secondary storage array depends on the storage vendor. For links to vendor guides, see: {ref}`disaster-recovery-replication-setup`.

```{admonition} Potential data loss
:class: important

If there is non-replicated data remaining on the primary location, promoting the secondary array might cause some data loss. Consult the {ref}`storage vendor's documentation <disaster-recovery-replication-setup>` for further information.
```

(disaster-recovery-replication-recover)=
## Recover resources

After the secondary storage array has been promoted, you can start recovering the workload. Run the steps in {ref}`disaster-recovery` on the secondary LXD deployment.

When prompted to choose the pools to scan for unknown volumes, select the storage pool that was configured during the replication setup.

The instances and custom storage volumes are then recovered on the secondary LXD deployment. Use `lxc start` to bring up the instances that were originally running on the primary deployment.

(disaster-recovery-replication-add-pool)=
### Add missing pool

If the LXD storage pool at the secondary location exists only in the storage array and has not yet been created in LXD (as described in {ref}`disaster-recovery-replication-entities-pool`), you must recover it first.

Use the `lxc storage create` command to add the storage pool. This works for both single and clustered LXD deployments. For more information, see: {ref}`storage-create-pool`.

(disaster-recovery-replication-add-pool-cephrbd)=
#### Recover Ceph RBD pool

LXD's {ref}`Ceph RBD driver <storage-ceph>` uses a _placeholder_ volume to reserve the storage pool and ensure it isn't used more than once. For replication, this behavior can be ignored because the replicated pool must be recovered at the secondary location. To allow this, set {config:option}`storage-ceph-pool-conf:source.recover` to ignore the placeholder volume if it was also replicated to the secondary location.

When creating the storage pool in a LXD cluster, make sure to add the `source.recover=true` setting when creating the pending storage pools per cluster member as this setting is cluster member specific.

(disaster-recovery-replication-failback)=
## Demote secondary and fail back to primary location

Once the primary location is back online, the storage layer ensures data consistency because the secondary storage array
now acts as the source of truth and no longer receives updates from the primary array. As long as this replication flow is not reversed, the running instances and custom volumes on the secondary location are protected.

```{admonition} Risk of network conflicts
:class: warning
Network collisions might occur if the primary location comes back online and LXD automatically starts up any instances. This issue is outside the scope of storage replication, but you must place appropriate measures to prevent such conflicts.
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
