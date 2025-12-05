(exp-csi)=
# The LXD CSI driver

The LXD CSI driver is an open source implementation of the [Container Storage Interface (CSI)](https://github.com/container-storage-interface/spec/blob/master/spec.md). It integrates LXD storage backends with Kubernetes.

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
Volume expansion          | &#x2713;        | {ref}`storage-drivers-local`, {ref}`storage-drivers-remote`, and {ref}`storage-drivers-shared` | Allows increasing the storage volume capacity. Block volumes can be expanded only while offline (detached), whereas filesystem volumes can be expanded while online (attached).
Volume snapshots          | &#x2713;        | {ref}`storage-drivers-local`, {ref}`storage-drivers-remote`, and {ref}`storage-drivers-shared` | Allows creating storage volume snapshots. This also requires snapshot custom resource definition (CRD).
Volume cloning            | &#x2713;        | {ref}`storage-drivers-local`, {ref}`storage-drivers-remote`, and {ref}`storage-drivers-shared` | Allows using existing storage volume as a source for a new one.
Topology-aware scheduling | &#x2713;        | {ref}`storage-drivers-local`                                                                   | Access to local volumes is by default restricted to nodes on the same LXD cluster member. The driver sets topology constraints accordingly so the scheduler can place Pods on compatible nodes.

(exp-csi-architecture)=
## Architecture

The LXD CSI driver follows the Container Storage Interface (CSI) model.
It is deployed in Kubernetes as a set of controller and node components that interact with the Kubernetes API server, Kubelet, and the LXD API to provision and manage storage volumes.

The diagram below illustrates the main components and their interactions:

```{figure} /images/csi/architecture.svg
:width: 100%
:alt: LXD CSI driver architecture
```

(exp-csi-architecture-components)=
### Components

The LXD CSI driver primarily consists of the controller and node services.
The controller service is responsible for external volume management operations, such as creating storage volumes and attaching them to the Kubernetes nodes.
The node service, on the other hand, handles internal node operations, such as mounting attached volume to the desired Kubernetes Pod.

(exp-csi-architecture-devlxd)=
#### LXD and DevLXD

LXD provides the storage backend for the LXD CSI driver.

The driver primarily interacts with the {ref}`DevLXD API <dev-lxd>`, which exposes LXD functionality to local processes inside an instance through the `/dev/lxd/sock` Unix socket.
In virtual machines, a LXD agent running inside the guest VM intercepts requests on this socket and delivers them to the DevLXD API over a Vsock connection, providing the same experience as in containers.

When a request is received, the DevLXD API first verifies that the caller is authorized to perform the requested operation on the target entity (for example, a storage volume).
If authorized, the corresponding handler in the main {ref}`LXD API <rest-api>` is invoked to execute the operation against the configured storage backend.

(exp-csi-architecture-cp)=
#### Control plane components

(exp-csi-architecture-cp-k8s-api)=
##### Kubernetes API server

At the core of Kubernetes, the [API server &#8599;](https://kubernetes.io/docs/concepts/overview/kubernetes-api/) acts as the single source of truth for cluster state.
It stores objects such as Pods, PersistentVolumeClaims (PVCs), PersistentVolumes (PVs), and VolumeAttachments.
All CSI components, Kubelets, and controllers observe the API server for changes and reconcile state accordingly.

(exp-csi-architecture-cp-controller)=
##### CSI controller service

The CSI controller service implements the controller-side Remote Procedure Calls (RPCs) defined by the CSI specification. It runs as a Kubernetes Deployment and communicates with the LXD API through the DevLXD socket. The controller is responsible for creating and deleting volumes, as well as attaching and detaching them from nodes.

Alongside the controller run the CSI controller sidecars, helper containers maintained by the Kubernetes CSI project. These integrate the controller with Kubernetes resources.

- `external-provisioner`: watches PVCs and PVs and invokes volume creation or deletion.
- `external-attacher`: watches VolumeAttachment objects and invokes volume attachment or detachment.
- `external-resizer`: watches for PVC updates and triggers volume expansion when user requests more storage on a PVC object.
- `livenessprobe`:  exposes an HTTP `/healthz` endpoint used by the Kubelet as a liveness probe to monitor the health of the CSI driver.

Leader election ensures that only one replica of the controller sidecars is active at a time.

(exp-csi-architecture-node)=
#### Node components

(exp-csi-architecture-node-kubelet)=
##### Kubelet

On every worker node, the [Kubelet &#8599;](https://kubernetes.io/docs/concepts/architecture/#kubelet) monitors the API server for Pods scheduled to run on that node. Before starting Pod containers, it invokes the CSI node plugin to stage and publish any required volumes.

(exp-csi-architecture-node-node)=
##### CSI node service

The CSI node service runs as a DaemonSet on every worker node and implements the node-side RPCs of the CSI specification.
It bind-mounts volumes into Pods when requested and cleans up mount points when Pods are deleted.

Supporting the node service are the node CSI sidecars, most notably the `node-driver-registrar`, which registers the plugin with Kubelet so that it can receive volume operations.

The node service also communicates with the local DevLXD API socket to determine which LXD cluster member the node is running on.
This information is used to configure topology constraints, ensuring that the Kubernetes scheduler only places Pods on nodes that can access the required volumes, since local volumes created on one cluster member cannot be attached to another.

(exp-csi-architecture-k8s-primitives)=
### Relation to Kubernetes primitives

The LXD CSI driver integrates directly with standard Kubernetes storage objects and translates them into LXD operations.

(exp-csi-architecture-k8s-primitives-sc)=
#### StorageClass

A [StorageClass &#8599;](https://kubernetes.io/docs/concepts/storage/storage-classes/), as defined for the LXD CSI driver, represents a LXD storage pool where volumes are created and managed.
The StorageClass also defines default settings applied to every volume created with that StorageClass.
These settings cover provisioning timing, volume parameters, mount options, and reclaim behavior.
When a PersistentVolumeClaim references a StorageClass, the driver provisions the volume in the selected LXD storage pool using those settings.
Multiple StorageClasses can reference the same LXD storage pool while keeping different defaults.

(exp-csi-architecture-k8s-primitives-pvc)=
#### PersistentVolumeClaim (PVC)

A [PVC &#8599;](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims) represents a user request for storage volume.
When a PVC references a StorageClass for the LXD CSI driver, the `external-provisioner` sidecar detects it and invokes the driver’s controller over RPC to create the volume in the configured LXD storage pool.

(exp-csi-architecture-k8s-primitives-pv)=
#### PersistentVolume (PV)

Each PVC that is successfully provisioned is bound to a [PV &#8599;](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistent-volumes).
The PV contains metadata such as capacity, access mode, and an identifier managed by the driver, referencing the LXD volume.
The PV therefore serves as the Kubernetes-side representation of the LXD volume.

(exp-csi-architecture-k8s-primitives-va)=
#### VolumeAttachment

When a volume is attached to a node, Kubernetes creates a [VolumeAttachment &#8599;](https://kubernetes.io/docs/reference/kubernetes-api/config-and-storage-resources/volume-attachment-v1/) object to track the relationship between a volume and the node.
The `external-attacher` sidecar watches these objects and invokes the driver's controller to attach or detach the volume as needed.
With the LXD CSI driver, this attaches or detaches the LXD volume to the target LXD instance.

(exp-csi-architecture-k8s-primitives-vsclass)=
#### VolumeSnapshotClass

A [VolumeSnapshotClass &#8599;](https://kubernetes.io/docs/concepts/storage/volume-snapshot-classes/) is a snapshot equivalent of StorageClass.
It references a CSI driver to use when creating volume snapshots, the snapshot deletion policy, and any additional driver-specific parameters.

(exp-csi-architecture-k8s-primitives-vs)=
#### VolumeSnapshot

A [VolumeSnapshot &#8599;](https://kubernetes.io/docs/concepts/storage/volume-snapshots) represents a user request for a snapshot of a specific PVC.
Each volume snapshot references a VolumeSnapshotClass and a source PVC.
The snapshot controller invokes the CSI driver to create a snapshot from a source volume, using the parameters defined in this class.

(exp-csi-architecture-k8s-primitives-vsc)=
#### VolumeSnapshotContent

A [VolumeSnapshotContent &#8599;](https://kubernetes.io/docs/concepts/storage/volume-snapshots/#volume-snapshot-contents) represents the actual snapshot object created by the CSI driver.
For the LXD CSI driver, it represents a LXD volume snapshot, similar to how a PersistentVolume represents the actual LXD volume.
It stores driver-managed identifiers and metadata for the snapshot and is bound to the VolumeSnapshot that requested it.

(exp-csi-lifecycle)=
## Life cycle

```{figure} /images/csi/lifecycle.svg
:width: 100%
:alt: LXD CSI driver life cycle
```

The diagram above illustrates how a Pod with a PersistentVolumeClaim (PVC) progresses through the CSI volume life cycle.
It shows the interactions between the Kubernetes control plane, the LXD CSI driver, and the LXD storage backend.

1. An administrator creates a Pod that references a PVC.

1. The Kubernetes scheduler assigns the Pod to a specific worker node.

1. The `external-provisioner` sidecar requests volume creation from the CSI controller. If the volume is successfully created, the external-attacher similarly requests the volume to be attached to the previously selected node.

1. The request is authorized by the DevLXD API, which verifies that the desired operation is allowed to be executed on a particular LXD entity.

1. Upon successful authorization, the request is forwarded to the main LXD API.

1. The LXD API creates the requested volume in the configured storage pool.

1. The volume is attached to the node where the Pod was previously scheduled.

1. Kubelet invokes the CSI node service requesting the attached volume to be mounted into the Pod.

1. The node service bind-mounts the volume into the Pod.

1. With the volume mounted and available, Kubelet starts the Pod’s containers.

When the Pod and PVC are deleted, these steps run in reverse order.
The volume is unpublished, detached from the node, and finally deleted from the LXD storage pool.

(exp-csi-security)=
## Security

The LXD CSI driver relies on the {ref}`DevLXD APIs <dev-lxd>` for volume management.
These APIs are disabled by default and must be explicitly enabled on each instance that is hosting a Kubernetes node through the {config:option}`instance-security:security.devlxd` and {config:option}`instance-security:security.devlxd.management.volumes` configuration options.

DevLXD enforces project-level isolation, allowing access to LXD entities only within a project where an invoked instance is running.
As a result, each Kubernetes cluster must run inside a single LXD {ref}`project <exp-projects>`.

Operations that require elevated permissions (such as creating or attaching volumes) use token-based authentication.
The token, stored as a Kubernetes Secret, identifies the caller and is checked by LXD’s {ref}`fine-grained-authorization` system.
Ownership of volumes, snapshots, and devices is tracked in the LXD configuration and ensures the identity can later manage only entities it has previously created.

## Related topics

{{csi_how}}

{{csi_ref}}
