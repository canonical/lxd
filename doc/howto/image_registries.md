---
myst:
  html_meta:
    description: Create, view, configure, and delete LXD image registries.
---

(howto-image-registries)=
# How to manage image registries

Image registries store information about global, read-only sources of images, such as SimpleStreams servers or other LXD servers.
You can use image registries to centrally define and manage the image sources that are accessible to all members of the cluster.
Once you create an image registry, you can use it to download images for LXD.

(howto-image-registries-create)=
## Create an image registry

The requirements for creating a new image registry depend on the protocol used by the image source.

To create a registry for an image source that uses the `lxd` protocol, you must connect to the remote LXD server through a {ref}`cluster link <howto-cluster-links-create>`.
Use a public cluster link for a public LXD server, or a unidirectional or bidirectional cluster link to authenticate to a private server.

`````{tabs}
````{group-tab} CLI

Use the required `cluster` configuration key to specify the cluster link:

```bash
lxc image registry create <registry_name> cluster=<cluster_link_name> source_project=<project>
```

````
````{group-tab} API

Send a `POST` request to the `/1.0/image-registries` endpoint, specifying the required `cluster` configuration key:

```bash
lxc query --request POST /1.0/image-registries --data '{"name": "my-registry", "config": {"cluster": "my-cluster-link", "source_project": "default"}}'
```

See [`POST /1.0/image-registries`](swagger:/image-registries/image_registries_post) for more information.

````
`````

To create a registry for an image source that uses the `simplestreams` protocol, specify the URL of the SimpleStreams server.

`````{tabs}
````{group-tab} CLI

Use the required `url` configuration key to specify the SimpleStreams server:

```bash
lxc image registry create <registry_name> url=<URL>
```

````
````{group-tab} API

Send a `POST` request to the `/1.0/image-registries` endpoint, specifying the required `url` configuration key:

```bash
lxc query --request POST /1.0/image-registries --data '{"name": "my-registry", "config": {"url": "https://images.example.org"}}'
```

See [`POST /1.0/image-registries`](swagger:/image-registries/image_registries_post) for more information.

````
`````

(howto-image-registries-view)=
## View image registries

`````{tabs}
````{group-tab} CLI

To list all configured image registries, run:

```bash
lxc image registry list
```

To view the configuration of a specific image registry, run:

```bash
lxc image registry show <registry_name>
```

````
````{group-tab} API

To list all image registries, send the following request:

```bash
lxc query --request GET /1.0/image-registries
```

To view the configuration of a specific image registry, send the following request:

```bash
lxc query --request GET /1.0/image-registries/<name>
```

See [`GET /1.0/image-registries`](swagger:/image-registries/image_registries_get) and [`GET /1.0/image-registries/{name}`](swagger:/image-registries/{name}/image_registry_get) for more information.

````
`````

(howto-image-registries-configure)=
## Configure an image registry

You can edit the configuration for an image registry in your text editor or set specific configuration options.

`````{tabs}
````{group-tab} CLI

To edit an image registry in your default text editor, run:

```bash
lxc image registry edit <registry_name>
```

To set a specific configuration option, run:

```bash
lxc image registry set <registry_name> <key> <value>
```

For example, to update the source project:

```bash
lxc image registry set my-registry source_project foo
```

````
````{group-tab} API

To update an image registry, send a `PUT` or `PATCH` request to the `/1.0/image-registries/<name>` endpoint.

```bash
lxc query --request PATCH /1.0/image-registries/my-registry --data '{"description": "New description"}'
```

See [`PUT /1.0/image-registries/{name}`](swagger:/image-registries/{name}/image_registry_put) and [`PATCH /1.0/image-registries/{name}`](swagger:/image-registries/{name}/image_registry_patch) for more information.

````
`````

For a list of available configuration options, see {ref}`ref-image-registries`.

(howto-image-registries-delete)=
## Delete an image registry

To delete an image registry, run:

`````{tabs}
````{group-tab} CLI

```bash
lxc image registry delete <registry_name>
```

````
````{group-tab} API

```bash
lxc query --request DELETE /1.0/image-registries/<name>
```

See [`DELETE /1.0/image-registries/{name}`](swagger:/image-registries/{name}/image_registry_delete) for more information.

````
`````
