(exp-csi)=
# The LXD CSI driver

The LXD CSI driver is an open source implementation of the Container Storage Interface (CSI) that integrates LXD storage backends with Kubernetes.

It leverages LXD’s wide range of supported storage drivers, enabling dynamic provisioning of both local and remote volumes.
Depending on the storage pool, the CSI supports provisioning of both block and filesystem volumes.

The driver is compatible with standalone and clustered LXD deployments, including [MicroCloud](https://canonical.com/microcloud).

(exp-csi-capabilities)=
## Storage capabilities

The LXD CSI driver supports all {ref}`storage-drivers-local`, {ref}`storage-drivers-remote`, and {ref}`storage-drivers-shared` storage drivers provided by LXD.
The table below lists its capabilities.

Capability                | Supported       | Storage drivers                                                                                | Description
--------------------------|-----------------|------------------------------------------------------------------------------------------------|------------
Dynamic provisioning      | &#x2713;        | {ref}`storage-drivers-local`, {ref}`storage-drivers-remote`, and {ref}`storage-drivers-shared` | Volumes are created and deleted on demand through PersistentVolumeClaims.
Filesystem volumes        | &#x2713;        | {ref}`storage-drivers-local`, {ref}`storage-drivers-remote`, and {ref}`storage-drivers-shared` | Supported when the driver provides filesystem volumes.
Shared filesystem volumes | - (coming-soon) | {ref}`storage-drivers-shared`                                                                  | Allows attaching storage volume to multiple nodes simultaneously (through the use of volume access modes `ReadWriteMany` and `ReadOnlyMany`).
Block volumes             | &#x2713;        | {ref}`storage-drivers-local` and {ref}`storage-drivers-remote`                                 | Supported when the driver provides block volumes.
Volume expansion          | - (coming-soon) | {ref}`storage-drivers-local`, {ref}`storage-drivers-remote`, and {ref}`storage-drivers-shared` | Allows increasing the storage volume capacity. Block volumes can be expanded only while offline (detached), whereas filesystem volumes can be expanded while online (attached).
Volume snapshots          | - (coming-soon) | {ref}`storage-drivers-local`, {ref}`storage-drivers-remote`, and {ref}`storage-drivers-shared` | Allows creating storage volume snapshots. This also requires VolumeSnapshot custom resource definition (CRD).
Cloning                   | - (coming-soon) | {ref}`storage-drivers-local`, {ref}`storage-drivers-remote`, and {ref}`storage-drivers-shared` | Allows using existing storage volume as a source for a new one.
Topology-aware scheduling | &#x2713;        | {ref}`storage-drivers-local`                                                                   | Access to local volumes is by default restricted to nodes on the same LXD cluster member. The driver sets topology constraints accordingly so the scheduler can place Pods on compatible nodes.
