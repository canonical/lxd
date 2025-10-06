(howto-storage-csi)=
# How to use the LXD CSI driver with Kubernetes

Learn how to get the LXD Container Storage Interface (CSI) driver running in your Kubernetes cluster.

## Getting started

(howto-storage-csi-prerequisites)=
### Prerequisites

The primary requirement is a Kubernetes cluster (of any size), running on LXD instances inside a dedicated LXD {ref}`project <exp-projects>`.

This guide assumes you have administrative access to both LXD and the Kubernetes cluster, and that the LXD instances are running in a project named `lxd-csi-project`.

(howto-storage-csi-authorization)=
### Authorization

By default, the {ref}`DevLXD API <dev-lxd>` is not allowed to manage storage volumes or attach them to instances.
You must enable this by setting {config:option}`instance-security:security.devlxd.management.volumes` to `true` on all LXD instances where the CSI driver will be running.

For example, to enable DevLXD volume management on instance `node-1`, run:

```sh
lxc config set node-1 --project lxd-csi-project security.devlxd.management.volumes=true
```

You can also use a LXD profile to apply this setting to multiple instances at once.

```{admonition} Limitation
:class: note
LXD CSI is limited to Kubernetes clusters that are running within a single LXD project.
```

At this point, DevLXD is allowed to access the LXD endpoint for volume management, but the LXD CSI still needs to prove it is authorized to perform such actions.
We will create a DevLXD identity with sufficient permissions and issue a bearer token for it.

The identity must have permissions in the project where the Kubernetes nodes are running to:

+ view the project,
+ manage (view, create, edit, delete) storage volumes,
+ edit instances.

First, create a new authorization group `csi-group` with the required permissions:

```sh
lxc auth group create csi-group
lxc auth group permission add csi-group project lxd-csi-project can_view
lxc auth group permission add csi-group project lxd-csi-project storage_volume_manager
lxc auth group permission add csi-group project lxd-csi-project can_edit_instances
```

Next, create an identity `devlxd/csi` and assign the previously created group `csi-group` to it:

```sh
lxc auth identity create devlxd/csi --group csi-group
```

Finally, issue a new bearer token to be used by the CSI driver:

```sh
token=$(lxc auth identity token issue devlxd/csi --quiet)
```

(howto-storage-csi-deploy)=
### Deploying CSI driver

Create a namespace `lxd-csi`:

```sh
kubectl create namespace lxd-csi --save-config
```

Create a Kubernetes secret `lxd-csi-secret` containing a previously created bearer token:

```sh
kubectl create secret generic lxd-csi-secret \
    --namespace lxd-csi \
    --from-literal=token="${token}"
```

(howto-storage-csi-deploy-helm)=
#### Deploying CSI driver using Helm chart

The easiest way to deploy LXD CSI is using Helm chart:

```sh
helm install lxd-csi-driver oci://ghcr.io/canonical/charts/lxd-csi-driver \
  --version v0.0.0-latest-edge \
  --namespace lxd-csi
```

```{tip}
Use the `template` command instead of `install` to see the resulting manifests.
```

The chart is configured to work *out of the box*. It deploys the CSI node server as a DaemonSet, with the CSI controller server as a single replica Deployment, and ensures minimal required access is granted to the CSI driver.

You can tweak the chart to create your desired storage classes, set resource limits, and increase the controller replica count by providing custom chart values.
The easiest way to get available values is by fetching the chart's default values:

```sh
helm show values oci://ghcr.io/canonical/charts/lxd-csi-driver --version v0.0.0-latest-edge > values.yaml
```

```{tip}
Use the `--values` flag with Helm commands to apply your modified values file.
```

(howto-storage-csi-deploy-manual)=
#### Deploying CSI driver using manifests

Alternatively, you can deploy the LXD CSI controller and node servers from manifests that can be found in the [deploy](https://github.com/canonical/lxd-csi-driver/tree/main/deploy) directory of the LXD CSI Driver GitHub repository.

```sh
git clone https://github.com/canonical/lxd-csi-driver
cd lxd-csi-driver
kubectl apply -f deploy/
```
