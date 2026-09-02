---
discourse: "[New&#32;LXD&#32;image&#32;server&#32;available&#32;(images.lxd.canonical.com)](43824),[Image&#32;server&#32;infrastructure](16647)"
relatedlinks: "[Deploying&#32;a&#32;new&#32;LXD&#32;image&#32;server&#32;-&#32;YouTube](https://www.youtube.com/watch?v=pM0EgUqj2a0)"
---

(remote-image-servers)=
# Built-in image registries

LXD downloads images through {ref}`image registries <ref-image-registries>`.
Every LXD server comes pre-configured with the following built-in image registries, which point to the default public image sources:

`images`
: This registry provides unofficial images for a variety of Linux distributions.
  The images are built to be compact and minimal, and therefore the default image variants do not include `cloud-init`.
  Where possible, `/cloud` variants that include `cloud-init` are provided.
  See [`cloud-init` support in images](cloud-init-support).

  This registry does not provide official Ubuntu images (for those, use the `ubuntu` registry).
  It does, however, provide desktop variants of current Ubuntu releases.

  See [`images.lxd.canonical.com`](https://images.lxd.canonical.com) for an overview of available images.

`ubuntu`
: This registry provides official stable Ubuntu images.
  All images are cloud images, which means that they include both `cloud-init` and the `lxd-agent`.

  See [`cloud-images.ubuntu.com/releases`](https://cloud-images.ubuntu.com/releases/) for an overview of available images.

`ubuntu-daily`
: This registry provides official daily Ubuntu images.
  All images are cloud images, which means that they include both `cloud-init` and the `lxd-agent`.

  See [`cloud-images.ubuntu.com/daily`](https://cloud-images.ubuntu.com/daily/) for an overview of available images.

`ubuntu-minimal`
: This registry provides official Ubuntu Minimal images.
  All images are cloud images, which means that they include both `cloud-init` and the `lxd-agent`.

  See [`cloud-images.ubuntu.com/minimal/releases`](https://cloud-images.ubuntu.com/minimal/releases/) for an overview of available images.

`ubuntu-minimal-daily`
: This registry provides official daily Ubuntu Minimal images.
  All images are cloud images, which means that they include both `cloud-init` and the `lxd-agent`.

  See [`cloud-images.ubuntu.com/minimal/daily`](https://cloud-images.ubuntu.com/minimal/daily/) for an overview of available images.

The built-in registries all use the {ref}`simplestreams protocol <remote-image-server-types>`.
They are marked as built-in and cannot be renamed, reconfigured, or deleted.
To add further image sources, {ref}`create your own image registries <howto-image-registries>`.

## Related topics

{{images_how}}

{{images_exp}}
