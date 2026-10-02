---
myst:
  html_meta:
    description: How to set up storage replication to prepare for disaster recovery operations.
---

(howto-storage-replication-setup)=
# How to set up storage replication

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

If you need to create a storage pool at either location, see: {ref}`howto-storage-pools-create`.

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

(disaster-recovery-replication-cephrbd)=
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

