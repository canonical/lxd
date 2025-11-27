(howto-storage-csi)=
# How to use the LXD CSI driver with Kubernetes

Learn how to get the LXD Container Storage Interface (CSI) driver running in your Kubernetes cluster.

(howto-storage-csi-prerequisites)=
## Prerequisites

The primary requirement is a Kubernetes cluster (of any size), running on LXD instances inside a dedicated LXD {ref}`project <exp-projects>`.

This guide assumes you have administrative access to both LXD and the Kubernetes cluster.

(howto-storage-csi-authorization)=
## Authorization

By default, the {ref}`DevLXD API <dev-lxd>` is not allowed to manage storage volumes or attach them to instances.
You must enable this by setting {config:option}`instance-security:security.devlxd.management.volumes` to `true` on all LXD instances where the CSI driver will be running:

```sh
lxc config set <instance-name> --project <project-name> security.devlxd.management.volumes=true
```

For example, to enable DevLXD volume management on instance `node-1` in a project named `lxd-csi-project`, run:

```sh
lxc config set node-1 --project lxd-csi-project security.devlxd.management.volumes=true
```

You can also use a LXD profile to apply this setting to multiple instances at once.

```{admonition} Limitation
:class: note
The LXD CSI is limited to Kubernetes clusters that are running within a single LXD project.
```

At this point, DevLXD is allowed to access the LXD endpoint for volume management, but the LXD CSI still needs to prove it is authorized to perform such actions.
You must create a DevLXD identity with sufficient permissions and issue a bearer token for it.

The identity must have permissions in the project where the Kubernetes nodes are running to:

+ view the project,
+ manage (view, create, edit, delete) storage volumes,
+ edit instances.

First, create a new authorization group with the required permissions:

```sh
lxc auth group create <group-name>
lxc auth group permission add <group-name> project <project-name> can_view
lxc auth group permission add <group-name> project <project-name> storage_volume_manager
lxc auth group permission add <group-name> project <project-name> can_edit_instances
```

Example using a group named `csi-group` and a project named `lxd-csi-project`:

```sh
lxc auth group create csi-group
lxc auth group permission add csi-group project lxd-csi-project can_view
lxc auth group permission add csi-group project lxd-csi-project storage_volume_manager
lxc auth group permission add csi-group project lxd-csi-project can_edit_instances
```

Next, create a DevLXD identity and assign the previously created group to it:

```sh
lxc auth identity create devlxd/<identity-name> --group <group-name>
```

Example with a DevLXD identity named `csi` and a group named `csi-group`:

```sh
lxc auth identity create devlxd/csi --group csi-group
```

Finally, issue a new bearer token to be used by the CSI driver:

```sh
token=$(lxc auth identity token issue devlxd/<identity-name> --quiet)
```

To issue a bearer token for DevLXD identity named `csi`, run:

```sh
token=$(lxc auth identity token issue devlxd/csi --quiet)
```

(howto-storage-csi-deploy)=
## Deploy the CSI driver

First, create a new Kubernetes namespace named `lxd-csi`:

```sh
kubectl create namespace lxd-csi --save-config
```

Afterwards, create a Kubernetes secret `lxd-csi-secret` containing a previously created bearer token:

```sh
kubectl create secret generic lxd-csi-secret \
    --namespace lxd-csi \
    --from-literal=token="${token}"
```

(howto-storage-csi-deploy-helm)=
### Deploy the CSI driver using a Helm chart

You can deploy the LXD CSI using a Helm chart:

```sh
helm install lxd-csi-driver oci://ghcr.io/canonical/charts/lxd-csi-driver \
  --version v0 \
  --namespace lxd-csi
```

```{tip}
Use the `template` command instead of `install` to see the resulting manifests.
```

The chart is configured to work out of the box. It deploys the CSI node server as a DaemonSet, with the CSI controller server as a single replica Deployment, and ensures minimal required access is granted to the CSI driver.

You can tweak the chart to create your desired storage classes, set resource limits, and increase the controller replica count by providing custom chart values.
To get available values, fetch the chart's default values:

```sh
helm show values oci://ghcr.io/canonical/charts/lxd-csi-driver --version v0 > values.yaml
```

```{tip}
Use the `--values` flag with Helm commands to apply your modified values file.
```

(howto-storage-csi-usage)=
## Usage examples

This section provides practical examples of configuring StorageClass and PersistentVolumeClaim (PVC) resources when using the LXD CSI driver.

The examples cover:

+ Creating different types of storage classes,
+ Defining volume claims that request storage from those classes,
+ Demonstrating how different Kubernetes resources consume the volumes.

(howto-storage-csi-usage-storageclass)=
### StorageClass configuration

The following example demonstrates how to configure a Kubernetes StorageClass that uses the LXD CSI driver for provisioning volumes.

In the StorageClass, the fields `provisioner` and `parameters.storagePool` are required.
The first specifies the name of the LXD CSI driver, which defaults to `lxd.csi.canonical.com`, and the second references a target storage pool where the driver will create volumes.

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: lxd-csi-fs
provisioner: lxd.csi.canonical.com  # Name of the LXD CSI driver.
parameters:
  storagePool: my-lxd-pool          # Name of the target LXD storage pool.
```

(howto-storage-csi-usage-storageclass-default)=
#### Default StorageClass

The default StorageClass is used when `storageClass` is not explicitly set in the PVC configuration.
You can mark a Kubernetes StorageClass as the default by setting the `storageclass.kubernetes.io/is-default-class: "true"` annotation.

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: lxd-csi-sc
  annotations:
    storageclass.kubernetes.io/is-default-class: "true"
provisioner: lxd.csi.canonical.com
parameters:
  storagePool: my-lxd-pool
```

(howto-storage-csi-usage-storageclass-volume-binding)=
#### Immediate volume binding

By default, volume binding is set to `WaitForFirstConsumer`, which delays volume creation until the Pod is scheduled.
Setting the volume binding mode to `Immediate` instructs Kubernetes to provision the volume as soon as the PVC is created.

```{admonition} Immediate binding with local storage volumes
:class: warning
When using {ref}`local <storage-drivers-local>` storage volumes, the immediate volume binding mode can cause the Pod to be scheduled on a node without access to the volume, leaving the Pod in a `Pending` state.
```

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: lxd-csi-immediate
provisioner: lxd.csi.canonical.com
volumeBindingMode: Immediate        # Default is "WaitForFirstConsumer"
parameters:
  storagePool: my-lxd-pool
```

(howto-storage-csi-usage-storageclass-volume-reclaim)=
#### Prevent volume deletion

By default, the volume is deleted when its PVC is removed.
Setting the reclaim policy to `Retain` prevents the CSI driver from deleting the underlying LXD volume, allowing for manual cleanup or data recovery later.

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: lxd-csi-retain
provisioner: lxd.csi.canonical.com
reclaimPolicy: Retain               # Default is "Delete"
parameters:
  storagePool: my-lxd-pool
```

#### Volume expansion

Volume expansion can be enabled through StorageClass configuration.
When enabled, storage volume capacity can be increased once the volume is created.

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: lxd-csi-retain
provisioner: lxd.csi.canonical.com
allowVolumeExpansion: true          # Default is "false".
parameters:
  storagePool: my-lxd-pool
```

For filesystem volumes, expansion is performed online and does not require shutting down any Pod using the PVC.
Block volumes, however, only support offline expansion, meaning the Pod consuming the volume must be stopped before the volume’s capacity can be increased.

(howto-storage-csi-usage-storageclass-helm)=
#### Configure StorageClass using Helm chart

The LXD CSI Helm chart allows defining multiple storage classes as part of the deployment.
Each entry in the `storageClasses` list must include at least `name` and `storagePool`.

```yaml
# values.yaml
storageClasses:
- name: lxd-csi-fs            # (required) Name of the StorageClass.
  storagePool: my-pool        # (required) Name of the target LXD storage pool.
- name: lxd-csi-fs-retain
  storagePool: my-pool
  reclaimPolicy: Retain       # (optional) Reclaim policy for released volume. Defaults to "Delete".
  allowVolumeExpansion: true  # (optional) Whether to allow volume expansion. Defaults to "true".
```

(howto-storage-csi-usage-pvc)=
### PersistentVolumeClaim configuration

A PVC requests a storage volume from a StorageClass.
Specify the access modes, volume size (capacity), and volume mode.

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: app-data
spec:
  accessModes:
    - ReadWriteOnce             # Allowed storage volume access modes.
  storageClassName: lxd-csi-sc  # Storage class name.
  resources:
    requests:
      storage: 10Gi             # Storage volume size.
  volumeMode: Filesystem        # Storage volume mode (content type in LXD terminology). Can be "Filesystem" or "Block".
```

(howto-storage-csi-usage-pvc-access-modes)=
#### Access modes

[Access modes &#8599;](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#access-modes) define how a volume can be mounted by Pods.

Access mode        | Supported drivers                                                                              | Description
-------------------|------------------------------------------------------------------------------------------------|------------
`ReadWriteOnce`    | {ref}`storage-drivers-local`, {ref}`storage-drivers-remote`, and {ref}`storage-drivers-shared` | Mounted as read-write by a single node. Multiple Pods on that node can share it.
`ReadWriteOncePod` | {ref}`storage-drivers-local`, {ref}`storage-drivers-remote`, and {ref}`storage-drivers-shared` | Mounted as read-write by a single Pod on a single node.
`ReadOnlyMany`     | {ref}`storage-drivers-shared`                                                                  | Mounted as read-only by many Pods across nodes.
`ReadWriteMany`    | {ref}`storage-drivers-shared`                                                                  | Mounted as read-write by many Pods across nodes.

(howto-storage-csi-usage-pvc-cloning)=
#### Volume cloning

[Volume cloning &#8599;](https://kubernetes.io/docs/concepts/storage/volume-pvc-datasource/) allows you to create a new PVC from an existing one.
The source and target PVCs must have the same `volumeMode`, and the target’s requested size must be equal to or larger than the source. Also note that Kubernetes allows volumes to be cloned only within the same namespace.

To create a clone, reference the source PVC under the `dataSource` field, as shown below:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: app-data
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: lxd-csi-sc
  resources:
    requests:
      storage: 10Gi             # Must be equal to or larger than the size of the source volume.
  volumeMode: Filesystem        # Must match the source volume mode.
  dataSource:
    kind: PersistentVolumeClaim
    name: pvc-1                 # Name of the source PVC.
```

(howto-storage-csi-usage-vsclass)=
### VolumeSnapshotClass configuration

The following example demonstrates how to configure a Kubernetes VolumeSnapshotClass that uses the LXD CSI driver for provisioning volume snapshots.

In a VolumeSnapshotClass, the only required fields are `driver` and `deletionPolicy`. The former identifies the LXD CSI driver, and the latter determines whether the underlying LXD snapshot is removed when the corresponding VolumeSnapshot object is deleted in Kubernetes.

```yaml
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshotClass
metadata:
  name: lxd-csi-snapshotclass
driver: lxd.csi.canonical.com       # Name of the LXD CSI driver.
deletionPolicy: Delete              # Possible values are "Retain" and "Delete" (default).
```

```{admonition} Requirement
:class: note
Using the {ref}`exp-csi-architecture-k8s-primitives-vsclass` requires installing Kubernetes snapshot custom resource definitions (CRDs). When using the {ref}`LXD CSI Helm chart <howto-storage-csi-deploy-helm>`, you can enable snapshot support by setting `snapshotter.enabled` to `true`. This installs the required CRDs and deploys the snapshot controller.
```

(howto-storage-csi-usage-vsclass-default)=
#### Default VolumeSnapshotClass

The default VolumeSnapshotClass is used when `volumeSnapshotClassName` is not explicitly set in the VolumeSnapshot configuration.
To mark a Kubernetes VolumeSnapshotClass as the default, set the `snapshot.storage.kubernetes.io/is-default-class: "true"` annotation.

```yaml
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshotClass
metadata:
  name: lxd-csi-snapshotclass-default
  annotations:
    snapshot.storage.kubernetes.io/is-default-class: "true"
driver: lxd.csi.canonical.com
```

(howto-storage-csi-usage-vsclass-reclaim)=
#### Prevent volume snapshot deletion

By default, the volume snapshot is deleted when the corresponding VolumeSnapshot is removed.
To prevent the CSI driver from deleting the underlying LXD volume snapshot and the corresponding Kubernetes `VolumeSnapshotContent` object, set the deletion policy to `Retain`. This allows for manual cleanup or data recovery later.

```yaml
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshotClass
metadata:
  name: lxd-csi-snapshotclass
driver: lxd.csi.canonical.com
deletionPolicy: Retain              # Default is "Delete".
```

(howto-storage-csi-usage-vs)=
### VolumeSnapshot configuration

A VolumeSnapshot requests a snapshot of the volume bound to the referenced PVC.
Set the fields `spec.volumeSnapshotClassName` and `spec.source.persistentVolumeClaimName` to the LXD CSI snapshot class to handle the snapshot and the PVC to snapshot, respectively.

If the snapshot is taken successfully, a corresponding VolumeSnapshotContent object is created.
It is bound to the VolumeSnapshot and represents the actual LXD volume snapshot.

```yaml
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: lxd-csi-pvc-snapshot
spec:
  volumeSnapshotClassName: lxd-csi-snapshotclass
  source:
    persistentVolumeClaimName: lxd-csi-pvc
```

```{admonition} Requirement
:class: note
Using the {ref}`exp-csi-architecture-k8s-primitives-vs` requires installing Kubernetes snapshot custom resource definitions (CRDs). When using the {ref}`LXD CSI Helm chart <howto-storage-csi-deploy-helm>`, you can enable snapshot support by setting `snapshotter.enabled` to `true`. This installs the required CRDs and deploys the snapshot controller.
```

(howto-storage-csi-usage-example)=
### End-to-end examples

(howto-storage-csi-usage-example-deployment)=
#### Referencing PVC in Deployment

This pattern is used when multiple Pods share the same persistent volume.
The PVC is created first and then referenced by name in the Deployment.

Each replica mounts the same volume, which is only safe when:

+ the volume's access mode allows multi-node access (`ReadWriteMany`, `ReadOnlyMany`), or
+ the Deployment has a single replica (`replicas: 1`), as shown below.

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: app-data
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: lxd-csi-sc
  resources:
    requests:
      storage: 10Gi
  volumeMode: Filesystem

---

apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
spec:
  replicas: 1 # Use a single replica for non-shared storage volumes.
  selector:
    matchLabels:
      app: app
  template:
    metadata:
      labels:
        app: app
    spec:
      containers:
        - name: app
          image: nginx:stable
          ports:
            - containerPort: 80
          volumeMounts:
            - name: data
              mountPath: /usr/share/nginx/html
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: app-data  # References PVC named "app-data".
```

(howto-storage-csi-usage-example-statefulset)=
#### Referencing PVC in StatefulSet

This pattern is used when each Pod requires its own persistent volume.
The `volumeClaimTemplates` section dynamically creates a PVC per Pod (e.g. `data-app-0`, `data-app-1`, `data-app-2`).
This ensures each Pod retains its volume through restarts and rescheduling.

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: app
spec:
  serviceName: app
  replicas: 3
  selector:
    matchLabels:
      app: app
  template:
    metadata:
      labels:
        app: app
    spec:
      containers:
        - name: app
          image: nginx:stable
          ports:
            - containerPort: 80
          volumeMounts:
            - name: data
              mountPath: /usr/share/nginx/html
  volumeClaimTemplates:
    # PVC template used for each replica in a stateful set.
    - metadata:
        name: data
      spec:
        accessModes:
          - ReadWriteOnce
        storageClassName: lxd-csi-sc
        resources:
          requests:
            storage: 5Gi
```

## Related topics

{{csi_exp}}

{{csi_ref}}
