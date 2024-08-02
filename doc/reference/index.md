(reference)=
# Reference

The reference material in this section provides technical descriptions of LXD.

(reference-general)=
## General information

Before you start using LXD, you should check the system requirements.
You should also be aware of the supported architectures, the available image servers, the format for images, and the environment used for containers.

```{toctree}
:titlesonly:
:maxdepth: 2

/requirements
/architectures
/reference/remote_image_servers
/reference/image_format
Container environment </container-environment>
```

(reference-config)=
## Configuration options

LXD is highly configurable.
Check the available configuration options for the LXD server and the different entities used in LXD.

```{toctree}
:titlesonly:
:maxdepth: 2
:includehidden:

Configuration option index </config-options>
/server
/explanation/instance_config
/reference/preseed_yaml_fields
/reference/projects
/reference/storage_drivers
/reference/networks
Cluster configuration </reference/cluster_member_config>
```

(reference-production)=
## Production setup

Once you are ready for production, make sure your LXD server is configured to support the required load.
You should also regularly {ref}`monitor the server metrics <metrics>`.

```{toctree}
:titlesonly:
:maxdepth: 2

Production server settings </reference/server_settings>
/reference/provided_metrics
```

## Fine-grained permissions

If you are managing user access via {ref}`fine-grained-authorization`, check which {ref}`permissions <permissions>` can be assigned to groups.

```{toctree}
:titlesonly:
:maxdepth: 1

/reference/permissions
```

(reference-api)=
## REST API

All communication between LXD and its clients happens using a RESTful API over HTTP.
Check the list of API extensions to see if a feature is available in your version of the API.

```{toctree}
:titlesonly:
:maxdepth: 2

/restapi_landing
```

(reference-manpages)=
## Man pages

`lxc` is the command line client for LXD.
Its usage is documented in the help pages for the `lxc` commands and subcommands.

```{toctree}
:titlesonly:
:maxdepth: 2

/reference/manpages
```

(reference-internal)=
## Implementation details

You don't need to be aware of the internal implementation details to use LXD.
However, advanced users might be interested in knowing what happens internally.

```{toctree}
:titlesonly:
:maxdepth: 2

/internals
```

```{toctree}
:hidden:

Project repository <https://github.com/canonical/lxd>
Image server <https://images.lxd.canonical.com>
```
