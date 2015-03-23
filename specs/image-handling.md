# Introduction
LXD uses an image based workflow. It comes with a built-in image store
where the user or external tools can import images.

Containers are then started from those images.

It's possible to spawn remote containers using local images or local
containers using remote images. In such cases, the image may be cached
on the target LXD.

# Caching
When spawning a container from a remote image, the remote image is
downloaded into the local image store with the cached bit set. The image
will be kept locally as a private image until either it's been unused
(no new container spawned) for the number of days set in
images.remote\_cache\_expiry or until the image's expiry is reached
whichever comes first.

LXD keeps track of image usage by updating the last\_use\_date image
property every time a new container is spawned from the image.

# Image format
The image format for LXD is a compressed tarball (xz recommended) with
the following structure:
 - metadata.yaml
 - rootfs/

The rootfs directory contains a full file system tree of what will become the container's /.

metadata.yaml contains information relevant to running the image under
LXD, at the moment, this contains:

    architecture: x86_64
    creation_date: 1424284563
    name: ubuntu-14.04-amd64-20150218
    properties:
      description: Ubuntu 14.04 LTS Intel 64bit
      os: Ubuntu
      release: [trusty, '14.04']

The architecture and creation\_date fields are mandatory, the properties
are just a set of default properties for the image. The os, release,
name and description fields while not mandatory in any way, should be
pretty common.
