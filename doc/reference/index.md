---
myst:
  html_meta:
    description: An index of reference guides for LXD, including configuration settings, REST API documentation, production setup, permissions, and internal details.
---

(reference)=
# Reference

The reference material in this section provides technical descriptions of LXD.

(reference-general)=
## General information

These guides include compatibility information for operating systems running in instances, and man pages for the `lxc` CLI.

```{toctree}
:titlesonly:
:maxdepth: 1

/requirements
/architectures
/guest-os-compatibility
Container environment </container-environment>
/reference/manpages
```

## Releases

Release notes and details about the LXD release cadence and its snap.

```{toctree}
:titlesonly:
:maxdepth: 1

/reference/release-notes/index
/reference/releases-snap
```

## Images

Reference information for remote image servers and the LXD image format.

```{toctree}
:titlesonly:
:maxdepth: 1

/reference/remote_image_servers
/reference/image_format
```

(reference-config)=
## Configuration options

LXD is highly configurable, with options available for major entities as well as permissions for access control.

```{toctree}
:titlesonly:
:maxdepth: 1
:includehidden:

Configuration option index </config-options>
/server
/explanation/instance_config
/reference/preseed_yaml_fields
/reference/projects
/reference/storage_drivers
/reference/networks
/reference/image_registries
/reference/placement_groups
/reference/clusters
/reference/replicator_config
/reference/permissions
```

(reference-production)=
## Production setup

The LXD server can be optimized for production workloads and can monitor server metrics.

```{toctree}
:titlesonly:
:maxdepth: 1

Production server settings </reference/server_settings>
/reference/provided_metrics
```

(reference-api)=
## API and integrations

LXD exposes a REST API for managing all resources. The LXD CSI driver integrates LXD storage backends with Kubernetes.

```{toctree}
:titlesonly:
:maxdepth: 1

/restapi_landing
/reference/driver_csi
```

(reference-internal)=
## Internal implementation details

These guides are primarily of interest to advanced users, contributors, and developers.

```{toctree}
:titlesonly:
:maxdepth: 1

/internals
```

```{toctree}
:hidden:

Project repository <https://github.com/canonical/lxd>
Image server <https://images.lxd.canonical.com>
```
