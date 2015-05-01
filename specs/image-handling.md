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
 - templates/

The rootfs directory contains a full file system tree of what will become the container's /.

The templates directory contains pongo2-formatted templates of files inside the container.

metadata.yaml contains information relevant to running the image under
LXD, at the moment, this contains:

    architecture: x86_64
    creation_date: 1424284563
    properties:
      description: Ubuntu 14.04 LTS Intel 64bit
      os: Ubuntu
      release:
        - trusty
        - 14.04
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

The architecture and creation\_date fields are mandatory, the properties
are just a set of default properties for the image. The os, release,
name and description fields while not mandatory in any way, should be
pretty common.

For templates, the "when" key can be one or more of:
 - create (run at the time a new container is created from the image)
 - rename (run when a container is renamed)
 - copy (run when a container is copied to a different host)
 - move (run when a container is moved to a different host)
 - start (run every time the container is started)

When moving or copying a container to a different machine and also
changing its name in the process, both the copy/move templates and the
rename templates will be applied.

The templates will always receive the following context:
 - trigger: name of the event which triggered the template (string)
 - path: path of the file being templated (string)
 - container: key/value map of container properties (name, architecture and ephemeral) (map[string]string)
 - config: key/value map of the container's configuration (map[string]string)
 - devices: key/value map of the devices assigned to this container (map[string]map[string]string)
 - properties: key/value map of the template properties specified in metadata.yaml (map[string]string)

As a general rule, you should never template a file which is owned by a
package or is otherwise expected to be overwritten by normal operation
of the container.
