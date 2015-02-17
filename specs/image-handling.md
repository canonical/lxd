# Introduction
LXD uses an image based workflow. It comes with a built-in image store
where the user or external tools can import images.

Containers are then started from those images.

It's possible to spawn remote containers using local images or local
containers using remote images. In such cases, the image may be cached
on the target LXD.

# Image format
The image format for LXD is a compressed tarball (xz recommended) with
the following structure:
 - metadata.yaml
 - rootfs/

The rootfs directory contains a full file system tree of what will become the container's /.

metadata.yaml contains information relevant to running the image under
lxd, at the moment, this is an empty yaml document.
