---
discourse: 9310
---

(images-copy)=
# How to copy and import images

- Copy images from a remote (can also copy to another server)
- Import an image from a file

  - local file
  - file on web server


### Direct pushing of the image files

This is mostly useful for air-gapped environments where images cannot be
directly retrieved from an external server.

In such a scenario, image files can be downloaded on another system using:

    lxc image export ubuntu:22.04

Then transferred to the target system and manually imported into the
local image store with:

    lxc image import META ROOTFS --alias ubuntu-22.04

`lxc image import` supports both unified images (single file) and split
images (two files) with the example above using the latter.

### File on a remote web server

As an alternative to running a full image server only to distribute a
single image to users, LXD also supports importing images by URL.

There are a few limitations to that method though:

- Only unified (single file) images are supported
- Additional HTTP headers must be returned by the remote server

LXD will set the following headers when querying the server:

- `LXD-Server-Architectures` to a comma-separated list of architectures the client supports
- `LXD-Server-Version` to the version of LXD in use

And expects `LXD-Image-Hash` and `LXD-Image-URL` to be set by the remote server.
The former being the SHA256 of the image being downloaded and the latter
the URL to download the image from.

This allows for reasonably complex image servers to be implemented using
only a basic web server with support for custom headers.

On the client side, this is used with:

    lxc image import URL --alias some-name


## Import images
You can import images, that you:

- built yourself (see [Build Images](#build-images)),
- downloaded manually (see [Manual Download](#manual-download))
- exported from images or containers (see [Export Images](#export-images) and [Create Image from Containers](#create-image-from-containers))

#### Import container image

Components:

- lxd.tar.xz
- rootfs.squashfs

Use:

	lxc image import lxd.tar.xz rootfs.squashfs --alias custom-imagename


#### Import virtual-machine image

Components:

- lxd.tar.xz
- disk.qcow2

Use:

	lxc image import lxd.tar.xz disk.qcow2 --alias custom-imagename


### Manual download
You can also download images manually. For that you need to download the components described [above](#import-images).

#### From official LXD image server

!!! note
    It is easier to use the usual method with `lxc launch`. Use manual download only if you have a specific reason, like modification of the files before use for example.

**Link to official image server:**

[https://images.linuxcontainers.org/images/](https://images.linuxcontainers.org/images/)
