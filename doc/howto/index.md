---
myst:
  html_meta:
    description: An index of how-to guides for installing, configuring, and managing LXD, including instances, storage, networks, clustering, and production deployments.
---

(howtos)=
# How-to guides

These how-to guides cover key operations and processes in LXD.

## Set up the LXD server and initial access

LXD can be installed and initialized in multiple ways. Afterward, the server can be configured for network access through the CLI or UI client.

```{toctree}
:titlesonly:
:maxdepth: 1

/getting_started
/operation
```

## Work with LXD

Instances are created from images and can be either {ref}`system containers or virtual machines <containers-and-vms>`. Projects are useful for grouping related instances and managing user access.

```{toctree}
:titlesonly:
:maxdepth: 1

/instances
/images
/projects
/storage
/networks
```

## Get ready for production

For production deployments, clusters of LXD servers help support higher loads. The production setup guides also cover performance, monitoring, backup, and disaster recovery.

```{toctree}
:titlesonly:
:maxdepth: 1

/clustering
/production-setup
```

## Perform server administration

```{toctree}
:titlesonly:
:maxdepth: 1
Manage the snap </howto/snap>
Harden security </howto/security_harden>
/howto/troubleshoot
```

## Authenticate to the APIs

Bearer tokens can be used to authenticate to the LXD API; refer to {ref}`authentication` for other methods. The DevLXD API is used for communication between instances and their host.

```{toctree}
:titlesonly:
:maxdepth: 1

Authenticate to the LXD API using bearer tokens </howto/auth_bearer>
Authenticate to the DevLXD API </howto/devlxd_authenticate>
```

## Engage with us

```{toctree}
:titlesonly:
:maxdepth: 1

Get support </support>
Contribute to LXD </contributing>
```
