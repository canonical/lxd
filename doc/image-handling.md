# Image handling
## Introduction
LXD uses an image based workflow. It comes with a built-in image store
where the user or external tools can import images.

Containers are then started from those images.

It's possible to spawn remote instances using local images or local
instances using remote images. In such cases, the image may be cached
on the target LXD.

## Sources
LXD supports importing images from three different sources:

 - Remote image server (LXD or simplestreams)
 - Direct pushing of the image files
 - File on a remote web server

### Remote image server (LXD or simplestreams)
This is the most common source of images and the only one of the three
options which is supported directly at instance creation time.

With this option, an image server is provided to the target LXD server
along with any needed certificate to validate it (only HTTPS is supported).

The image itself is then selected either by its fingerprint (SHA256) or
one of its aliases.

From a CLI point of view, this is what's done behind those common actions:

 - lxc launch ubuntu:20.04 u1
 - lxc launch images:centos/8 c1
 - lxc launch my-server:SHA256 a1
 - lxc image copy images:gentoo local: --copy-aliases --auto-update

In the cases of `ubuntu` and `images` above, those remotes use
simplestreams as a read-only image server protocol and select images by
one of their aliases.

The `my-server` remote there is another LXD server and in that example
selects an image based on its fingerprint.

### Direct pushing of the image files
This is mostly useful for air-gapped environments where images cannot be
directly retrieved from an external server.

In such a scenario, image files can be downloaded on another system using:

 - lxc image export ubuntu:20.04

Then transferred to the target system and manually imported into the
local image store with:

 - lxc image import META ROOTFS --alias ubuntu-20.04

`lxc image import` supports both unified images (single file) and split
images (two files) with the example above using the latter.

### File on a remote web server
As an alternative to running a full image server only to distribute a
single image to users, LXD also supports importing images by URL.

There are a few limitations to that method though:

 - Only unified (single file) images are supported
 - Additional http headers must be returned by the remote server

LXD will set the following headers when querying the server:

 - `LXD-Server-Architectures` to a comma separate list of architectures the client supports
 - `LXD-Server-Version` to the version of LXD in use


And expects `LXD-Image-Hash` and `LXD-Image-URL` to be set by the remote server.
The former being the SHA256 of the image being downloaded and the latter
the URL to download the image from.

This allows for reasonably complex image servers to be implemented using
only a basic web server with support for custom headers.


On the client side, this is used with:

`lxc image import URL --alias some-name`

### Publishing an instance or snapshot as a new image
An instance or one of its snapshots can be turned into a new image.
This is done on the CLI with `lxc publish`.

When doing this, you will most likely first want to cleanup metadata and
templates on the instance you're publishing using the `lxc config metadata`
and `lxc config template` commands. You will also want to remove any
instance-specific state like host SSH keys, dbus/systemd machine-id, ...

The publishing process can take quite a while as a tarball must be
generated from the instance and then be compressed. As this can be
particularly I/O and CPU intensive, publish operations are serialized by LXD.

## Caching
When spawning an instance from a remote image, the remote image is
downloaded into the local image store with the cached bit set. The image
will be kept locally as a private image until either it's been unused
(no new instance spawned) for the number of days set in
`images.remote_cache_expiry` or until the image's expiry is reached
whichever comes first.

LXD keeps track of image usage by updating the `last_used_at` image
property every time a new instance is spawned from the image.

## Auto-update
LXD can keep images up to date. By default, any image which comes from a
remote server and was requested through an alias will be automatically
updated by LXD. This can be changed with `images.auto_update_cached`.

On startup and then every 6 hours (unless `images.auto_update_interval`
is set), the LXD daemon will go look for more recent version of all the
images in the store which are marked as auto-update and have a recorded
source server.

When a new image is found, it is downloaded into the image store, the
aliases pointing to the old image are moved to the new one and the old
image is removed from the store.

The user can also request a particular image be kept up to date when
manually copying an image from a remote server.


If a new upstream image update is published and the local LXD has the
previous image in its cache when the user requests a new instance to be
created from it, LXD will use the previous version of the image rather
than delay the instance creation.

This behavior only happens if the current image is scheduled to be
auto-updated and can be disabled by setting `images.auto_update_interval` to 0.

## Profiles
A list of profiles can be associated with an image using the `lxc image edit`
command. After associating profiles with an image, an instance launched
using the image will have the profiles applied in order. If `nil` is passed
as the list of profiles, only the `default` profile will be associated with
the image. If an empty list is passed, then no profile will be associated
with the image, not even the `default` profile. An image's associated
profiles can be overridden when launching an instance by using the
`--profile` and the `--no-profiles` flags to `lxc launch`.

## Image format
LXD currently supports two LXD-specific image formats.

The first is a unified tarball, where a single tarball
contains both the instance root and the needed metadata.

The second is a split model, using two files instead, one containing
the root, the other containing the metadata.

The former is what's produced by LXD itself and what people should be
using for LXD-specific images.

The latter is designed to allow for easy image building from existing
non-LXD rootfs tarballs already available today.

### Unified tarball
Tarball, can be compressed and contains:

 - `rootfs/`
 - `metadata.yaml`
 - `templates/` (optional)

In this mode, the image identifier is the SHA-256 of the tarball.

### Split tarballs
Two (possibly compressed) tarballs. One for metadata, one for the rootfs.

`metadata.tar` contains:

 - `metadata.yaml`
 - `templates/` (optional)

`rootfs.tar` contains a Linux root filesystem at its root.

In this mode the image identifier is the SHA-256 of the concatenation of
the metadata and rootfs tarball (in that order).

### Supported compression
The tarball(s) can be compressed using bz2, gz, xz, lzma, tar (uncompressed) or
it can also be a squashfs image.

### Content
For containers, the rootfs directory (or tarball) contains a full file system tree of what will become the `/`.
For VMs, this is instead a `root.img` file which becomes the main disk device.

The templates directory contains pongo2-formatted templates of files inside the instance.

`metadata.yaml` contains information relevant to running the image under
LXD, at the moment, this contains:

```yaml
architecture: x86_64
creation_date: 1424284563
properties:
  description: Ubuntu 18.04 LTS Intel 64bit
  os: Ubuntu
  release: bionic 18.04
templates:
  /etc/hosts:
    when:
      - create
      - rename
    template: hosts.tpl
    properties:
      foo: bar
  /etc/hostname:
    when:
      - start
    template: hostname.tpl
  /etc/network/interfaces:
    when:
      - create
    template: interfaces.tpl
    create_only: true
```

The `architecture` and `creation_date` fields are mandatory, the properties
are just a set of default properties for the image. The os, release,
name and description fields while not mandatory in any way, should be
pretty common.

For templates, the `when` key can be one or more of:

 - `create` (run at the time a new instance is created from the image)
 - `copy` (run when an instance is created from an existing one)
 - `start` (run every time the instance is started)

The templates will always receive the following context:

 - `trigger`: name of the event which triggered the template (string)
 - `path`: path of the file being templated (string)
 - `container`: key/value map of instance properties (name, architecture, privileged and ephemeral) (map[string]string) (deprecated in favor of `instance`)
 - `instance`: key/value map of instance properties (name, architecture, privileged and ephemeral) (map[string]string)
 - `config`: key/value map of the instance's configuration (map[string]string)
 - `devices`: key/value map of the devices assigned to this instance (map[string]map[string]string)
 - `properties`: key/value map of the template properties specified in metadata.yaml (map[string]string)

The `create_only` key can be set to have LXD only only create missing files but not overwrite an existing file.

As a general rule, you should never template a file which is owned by a
package or is otherwise expected to be overwritten by normal operation
of the instance.

For convenience the following functions are exported to pongo templates:

 - `config_get("user.foo", "bar")` => Returns the value of `user.foo` or `"bar"` if unset.
