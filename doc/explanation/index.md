---
myst:
  html_meta:
    description: An index of explanatory guides for LXD, covering instance types, storage, networking, access management, clustering, and production setup.
---

(explanation)=
# Explanation

The explanatory guides in this section discuss the concepts used in LXD and help you understand how things fit together.

(explanation-concepts)=
## Important concepts

LXD's core concepts include its relationship with LXC and the instance types it supports: system containers and virtual machines.

```{toctree}
:titlesonly:

/explanation/lxd_lxc
/explanation/instances
```

(explanation-entities)=
## Entities in LXD

LXD uses several distinct entity types, including images, storage pools, networks, and projects. To learn how to use them, refer to the {ref}`howtos`.

```{toctree}
:titlesonly:

/image-handling
/explanation/storage
/explanation/networks
/database
/explanation/lxc_show_info
```

(explanation-iam)=
## Access management

LXD supports multiple methods for authenticating remote API clients and provides fine-grained authorization controls. Projects can also be used to scope and restrict access.

```{toctree}
:titlesonly:

/authentication
/explanation/authorization
/explanation/projects
```

(explanation-production)=
## Production setup

For scalable, reliable, and secure LXD deployments, these guides help you understand the key concepts around clustering, performance tuning, and security.

```{toctree}
:titlesonly:

/explanation/clusters
/explanation/replicators
/explanation/performance_tuning
/explanation/security
/explanation/bpf
```

(explanation-csi)=
## The LXD CSI driver

The LXD CSI driver is an open source implementation of the Container Storage Interface (CSI) that integrates LXD storage backends with Kubernetes.

```{toctree}
:titlesonly:

/explanation/csi
```
