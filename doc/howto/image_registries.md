---
myst:
  html_meta:
    description: Create, view, configure, and delete LXD image registries.
---

(howto-image-registries)=
# How to manage image registries

Image registries are global read-only sources of images. They provide a way to define image sources (like SimpleStreams or other LXD servers) that are accessible to all members of the cluster and are managed centrally.
Once created, an image registry can be used to download images for LXD.

(howto-image-registries-create)=
## Create an image registry

How to add an image registry depends on the protocol that the source uses.

`````{tabs}
````{group-tab} CLI

To add a public registry using the `lxd` protocol (another LXD server), run:

    lxc image registry create <registry_name> --protocol=lxd url=<URL> source_project=default

If the source LXD server is private and linked via a {ref}`cluster link <howto-cluster-links-manage>`, you can specify the cluster link name:

    lxc image registry create <registry_name> --protocol=lxd cluster=<cluster_link_name> source_project=<project>

To add a registry using the `simplestreams` protocol, run:

    lxc image registry create <registry_name> --protocol=simplestreams url=<URL>

````
````{group-tab} API

To add an image registry, send a `POST` request to the `/1.0/image-registries` endpoint.

    lxc query --request POST /1.0/image-registries --data '{"name": "my-registry", "protocol": "lxd", "config": {"url": "https://192.0.2.10:8443", "source_project": "default"}}'

See [`POST /1.0/image-registries`](swagger:/image-registries/image_registries_post) for more information.

````
`````

(howto-image-registries-view)=
## View image registries

`````{tabs}
````{group-tab} CLI

To list all configured image registries, run:

    lxc image registry list

To view the configuration of a specific image registry, run:

    lxc image registry show <registry_name>

````
````{group-tab} API

To list all image registries, send the following request:

    lxc query --request GET /1.0/image-registries

To view the configuration of a specific image registry, send the following request:

    lxc query --request GET /1.0/image-registries/<name>

See [`GET /1.0/image-registries`](swagger:/image-registries/image_registries_get) and [`GET /1.0/image-registries/{name}`](swagger:/image-registries/{name}/image_registry_get) for more information.

````
`````

(howto-image-registries-configure)=
## Configure an image registry

You can update the configuration for an image registry by editing it or by setting specific properties.

`````{tabs}
````{group-tab} CLI

To edit an image registry in your default text editor, run:

    lxc image registry edit <registry_name>

To set a specific configuration option, run:

    lxc image registry set <registry_name> <key> <value>

For example, to update the source project:

    lxc image registry set my-registry source_project foo

````
````{group-tab} API

To update an image registry, send a `PUT` or `PATCH` request to the `/1.0/image-registries/<name>` endpoint.

    lxc query --request PATCH /1.0/image-registries/my-registry --data '{"description": "New description"}'

See [`PUT /1.0/image-registries/{name}`](swagger:/image-registries/{name}/image_registry_put) and [`PATCH /1.0/image-registries/{name}`](swagger:/image-registries/{name}/image_registry_patch) for more information.

````
`````

Image registries have the following configuration options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group image-registry-image-registry-conf start -->
    :end-before: <!-- config group image-registry-image-registry-conf end -->
```

(howto-image-registries-delete)=
## Delete an image registry

To delete an image registry, run:

`````{tabs}
````{group-tab} CLI

    lxc image registry delete <registry_name>

````
````{group-tab} API

    lxc query --request DELETE /1.0/image-registries/<name>

See [`DELETE /1.0/image-registries/{name}`](swagger:/image-registries/{name}/image_registry_delete) for more information.

````
`````
