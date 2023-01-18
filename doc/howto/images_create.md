(images-create)=
# How to create images

- Publish from an instance or snapshot
- Use distrobuilder



### Publishing an instance or snapshot as a new image

An instance or one of its snapshots can be turned into a new image.
This is done on the CLI with `lxc publish`.

When doing this, you will most likely first want to cleanup metadata and
templates on the instance you're publishing using the `lxc config metadata`
and `lxc config template` commands. You will also want to remove any
instance-specific state like host SSH keys, `dbus/systemd machine-id`, ...

The publishing process can take quite a while as a tarball must be
generated from the instance and then be compressed. As this can be
particularly I/O and CPU intensive, publish operations are serialized by LXD.



### Create image from containers
See command:

	lxc publish

## Build images
For building your own images, you can use [`distrobuilder`](https://github.com/lxc/distrobuilder) (a tool developed by us).

### Install distrobuilder
You can install distrobuilder via snap or compile it manually:

#### Install via Snap
See [https://snapcraft.io/distrobuilder](https://snapcraft.io/distrobuilder).

#### Compile
See [Instructions on distrobuilder GitHub repo](https://github.com/lxc/distrobuilder/#installing-from-source).

### Write or edit a template
You need an image template (e.g. `ubuntu.yaml`) to give instructions to distrobuilder.

You can start by using one of the example templates below. Modify those templates so they fit your needs.

See [Template details](#template-details) below for an overview of configuration keys.

#### Example templates
Standard template (includes all available options): [https://github.com/lxc/distrobuilder/blob/master/doc/examples/scheme.yaml](https://github.com/lxc/distrobuilder/blob/master/doc/examples/scheme.yaml)

Official LXD templates for various distributions: [https://github.com/lxc/lxc-ci/tree/master/images](https://github.com/lxc/lxc-ci/tree/master/images)

#### Template details
You can define multiple keys in templates:


| Section: | Description: | Documentation: |
| ---      | ---          | ---            |
| `image`  | defines distribution, architecture, release etc.| see [image.md](https://github.com/lxc/distrobuilder/blob/master/doc/image.md) |
| `source` | defines main package source, keys etc. | see [source.md](https://github.com/lxc/distrobuilder/blob/master/doc/source.md) |
| `targets` | defines configs for specific targets (e.g. LXD-client, instances etc.) |  see [targets.md](https://github.com/lxc/distrobuilder/blob/master/doc/targets.md) |
| `files` | defines generators to modify files | see [generators.md](https://github.com/lxc/distrobuilder/blob/master/doc/generators.md) |
| `packages` | defines packages for install or removal; add repositories |   see [packages.md](https://github.com/lxc/distrobuilder/blob/master/doc/packages.md) |
| `actions` | defines scripts to be run after specific steps during image building |  see [actions.md](https://github.com/lxc/distrobuilder/blob/master/doc/actions.md) |
| `mappings` | maps different terms for architectures for specific distributions (e.g. x86_64: amd64) | see [mappings.md](https://github.com/lxc/distrobuilder/blob/master/doc/mappings.md) |


!!! note "Note for VMs"
	You should either build an image with cloud-init support (provides automatic size growth) or set a higher size in the template, because the standard size is relatively small (~4 GB). Alternatively you can also grow it manually.

### Build an image

#### Container image
Build a container image with:

	distrobuilder build-lxd filename [target folder]

Replace:

* `filename` - with a template file (e.g. `ubuntu.yaml`).
* (optional)`[target folder]` - with the path to a folder of your choice; if not set, distrobuilder will use the current folder

After the image is built, see [Import images](#import-images) for how to import your image to LXD.

See [Building.md on distrobuilder's GitHub repo](https://github.com/lxc/distrobuilder/blob/master/doc/building.md#lxd-image) for details.

#### Virtual machine image
Build a virtual machine image with:

	distrobuilder build-lxd filename --vm [target folder]

Replace:

* `filename` - with a template file (e.g. `ubuntu.yaml`).
* (optional)`[target folder]` - with the path to a folder of your choice; if not set, distrobuilder will use the current folder


After the image is built, see [Import images](#import-images) for how to import your image to LXD.

### More information
[Distrobuilder GitHub repo](https://github.com/lxc/distrobuilder)

[Distrobuilder documentation](https://github.com/lxc/distrobuilder/tree/master/doc)
