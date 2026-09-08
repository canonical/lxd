(ref-image-registries)=
# Image registries

An image registry is a global, read-only source of images that LXD can download images from.
Each registry points to an image source, such as a SimpleStreams server or another LXD server, and uses a protocol that matches that source.

See {ref}`howto-image-registries` for instructions on how to create and manage image registries, and {ref}`remote-image-servers` for the registries that are built in to LXD.

(remote-image-server-types)=
## Registry protocols

An image registry uses one of the following protocols, depending on the type of image source it points to:

`simplestreams`
: The image source is a pure image server that uses the [simple streams format](https://git.launchpad.net/simplestreams/tree/).
  The built-in registries are all `simplestreams` registries.

  You configure a `simplestreams` registry with the `url` of the image server (which must use HTTPS).

`lxd`
: The image source is another LXD server, reached through a {ref}`cluster link <howto-cluster-links-create>`.
  This can be a LXD server that is used solely to serve images, or a regular LXD server that also serves images in addition to running instances.

  You configure a `lxd` registry with the name of the `cluster` link that connects to the source server.
  Use a public cluster link for a public LXD server, or a unidirectional or bidirectional cluster link to authenticate to a private server.

For a public LXD server that is used solely to serve images, set the {config:option}`server-core:core.https_address` configuration option and mark the images that you want to share as `public` (see {ref}`server-expose` for more information).
For a private LXD server, restrict access to the remote API and configure an authentication method (see {ref}`authentication`).

## Configuration options

Image registries can be configured through a set of key/value configuration options.
See {ref}`howto-image-registries-configure` for instructions on how to set these options.

The following options are available:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group image-registry-image-registry-conf start -->
    :end-before: <!-- config group image-registry-image-registry-conf end -->
```

## Related topics

{{images_how}}

{{images_exp}}
