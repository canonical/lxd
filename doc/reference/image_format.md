(image-format)=
# Image format

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

`rootfs.tar` contains a Linux root file system at its root.

In this mode the image identifier is the SHA-256 of the concatenation of
the metadata and rootfs tarball (in that order).

### Supported compression

LXD supports a wide variety of compression algorithms for tarballs
though for compatibility purposes, `gzip` or `xz` should be preferred.

For split images, the rootfs file can also be SquashFS-formatted in the
container case. For virtual machines, the `rootfs.img` file is always
`qcow2` and can optionally be compressed using `qcow2`'s native compression.

### Content

For containers, the rootfs directory (or tarball) contains a full file system tree of what will become the `/`.
For VMs, this is instead a `rootfs.img` file which becomes the main disk device.

The templates directory contains Pongo2-formatted templates of files inside the instance.

`metadata.yaml` contains information relevant to running the image under
LXD, at the moment, this contains:

```yaml
architecture: x86_64
creation_date: 1424284563
properties:
  description: Ubuntu 22.04 LTS Intel 64bit
  os: Ubuntu
  release: jammy 22.04
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
are just a set of default properties for the image. The `os`, `release`,
`name` and `description` fields while not mandatory in any way, should be
pretty common.

For templates, the `when` key can be one or more of:

- `create` (run at the time a new instance is created from the image)
- `copy` (run when an instance is created from an existing one)
- `start` (run every time the instance is started)

The templates will always receive the following context:

- `trigger`: name of the event which triggered the template (string)
- `path`: path of the file that uses the template (string)
- `container`: key/value map of instance properties (name, architecture, privileged and ephemeral) (map[string]string) (deprecated in favor of `instance`)
- `instance`: key/value map of instance properties (name, architecture, privileged and ephemeral) (map[string]string)
- `config`: key/value map of the instance's configuration (map[string]string)
- `devices`: key/value map of the devices assigned to this instance (map[string]map[string]string)
- `properties`: key/value map of the template properties specified in `metadata.yaml` (map[string]string)

The `create_only` key can be set to have LXD only only create missing files but not overwrite an existing file.

As a general rule, you should never template a file which is owned by a
package or is otherwise expected to be overwritten by normal operation
of the instance.

For convenience the following functions are exported to Pongo2 templates:

- `config_get("user.foo", "bar")` => Returns the value of `user.foo` or `"bar"` if unset.
