(images-remote)=
# How to use remote images

Images are downloaded through {ref}`image registries <ref-image-registries>`.
The [`lxc`](lxc.md) CLI command is pre-configured with several {ref}`built-in image registries <remote-image-servers>` that provide the most common public image sources.

```{note}
In the CLI, *remotes* are used to connect to other LXD servers (see {ref}`remotes`), not to source images.
To add a new image source, {ref}`create an image registry <howto-image-registries>` instead of adding a remote.
For backwards compatibility, older CLI clients that still reference an image through a remote keep working as long as a matching image registry exists on the server.

- If you are using the API, you can interact with different LXD servers by using their exposed API addresses.
  See {ref}`server-authenticate` for instructions on how to authenticate with the servers.

  {ref}`images-manage` describes how to interact with images on any LXD server through the API.
- The UI is pre-configured with several image sources, but does not currently support adding other sources or managing remote images.

  You can see the available images (and which source they come from) when you select the base image for a new instance.
```

## List available images

To list the images provided by an image registry, use the `--registry` flag:

    lxc image list --registry <registry_name>

To see which image registries are available, run [`lxc image registry list`](lxc_image_registry_list.md).
See {ref}`howto-image-registries` for more information.

You can filter the results.
See {ref}`images-manage-filter` for instructions.

## Add an image source

To make a new image source available for download, {ref}`create an image registry <howto-image-registries-create>`.

- For a SimpleStreams image server, create a registry with the server's `url`.
- For another LXD server, create a registry that references a {ref}`cluster link <howto-cluster-links-create>`.

## Add a remote LXD server

You can add a LXD server as a remote to manage it over the network (for example, to copy images between servers).

<!-- Include start list remotes -->
To see all configured remote servers, enter the following command:

    lxc remote list
<!-- Include end list remotes -->

<!-- Include start add remotes -->
To add a LXD server as a remote, enter the following command:

    lxc remote add <remote_name> <IP|FQDN|URL|token> [flags]

Some authentication methods require specific flags (for example, use [`lxc remote add <remote_name> <IP|FQDN|URL> --auth-type=oidc`](lxc_remote_add.md) for OIDC authentication).
See {ref}`server-authenticate` and {ref}`authentication` for more information.

For example, enter the following command to add a remote through an IP address:

    lxc remote add my-remote 192.0.2.10

You are prompted to confirm the remote server fingerprint and then asked for the token.
<!-- Include end add remotes -->

## Reference an image

To reference an image, specify its image registry and its alias or fingerprint, separated with a colon.
For example:

    ubuntu:24.04
    ubuntu-minimal:24.04
    images:alpine/edge
    local:ed7509d7e83f

Here, `ubuntu`, `ubuntu-minimal`, and `images` are {ref}`built-in image registries <remote-image-servers>`, and `local` refers to an image in the local image store.

(images-remote-default)=
## Select a default remote

If you specify an image name without the name of the remote, the default image server is used.

To see which server is configured as the default image server, enter the following command:

    lxc remote get-default

To select a different remote as the default image server, enter the following command:

    lxc remote switch <remote_name>
