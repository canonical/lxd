(images-remote)=
# How to use remote images

- Link to {ref}`remote-image-servers`
- List remotes
- Add remotes
- Set a default remote
- Reference an image (`<remote>:<image>`)
- Search for images
- Configure caching



### Remote image server (LXD or simplestreams)

This is the most common source of images and the only one of the three
options which is supported directly at instance creation time.

With this option, an image server is provided to the target LXD server
along with any needed certificate to validate it (only HTTPS is supported).

The image itself is then selected either by its fingerprint (SHA256) or
one of its aliases.

From a CLI point of view, this is what's done behind those common actions:

    lxc launch ubuntu:22.04 u1
    lxc launch images:centos/8 c1
    lxc launch my-server:SHA256 a1
    lxc image copy images:gentoo local: --copy-aliases --auto-update

In the cases of `ubuntu` and `images` above, those remotes use
simplestreams as a read-only image server protocol and select images by
one of their aliases.

The `my-server` remote there is another LXD server and in that example
selects an image based on its fingerprint.

## Use remote image servers
The easiest way is to use a built-in remote image server.

You can get a list of built-in image servers with:

	lxc remote list

LXD comes with 3 default servers:

 1. `ubuntu:` (for stable Ubuntu images)
 2. `ubuntu-daily:` (for daily Ubuntu images)
 3. `images:` (for a [bunch of other distros](https://images.linuxcontainers.org))

### List images on server

To get a list of remote images on server `images`, type:

	lxc image list images:

**Details:**

_Most details in the list should be self-explanatory._

- Alias with `cloud`: refers to images with built-in cloud-init support (see [Advanced Guide - Cloud-Init](/lxd/advanced-guide#cloud-init) and [official cloud-init documentation](https://cloudinit.readthedocs.io/en/latest/))

### Search for images
You can search for images, by applying specific elements (e.g. the name of a distribution).

Show all Debian images:

	lxc image list images: debian

Show all 64-bit Debian images:

	lxc image list images: debian amd64

## Images for virtual machines
It is recommended to use the `cloud` variants of images (visible by the `cloud`-tag in their `ALIAS`). They include cloud-init and the LXD-agent. They also increase their size automatically and are tested daily.


## Remote servers
See [Image handling](/lxd/docs/master/image-handling/) for detailed information.

LXD supports different kinds of remote servers:

* `Simple streams servers`: Pure image servers that use the [simple streams format](https://git.launchpad.net/simplestreams/tree/).
* `Public LXD servers`: Empty LXD servers with no storage pools and no networks that serve solely as image servers. Set the `core.https_address` configuration option (see [Server configuration](/lxd/docs/master/server/#server-configuration)) to `:8443` and do not configure any authentication methods to make the LXD server publicly available over the network on port 8443. Then set the images that you want to share to `public`.
* `LXD servers`: Regular LXD servers that you can manage over a network, and that can also be used as image servers. For security reasons, you should restrict the access to the remote API and configure an authentication method to control access. See [Access to the remote API](/lxd/docs/master/security/#access-to-the-remote-api) and [Remote API authentication](/lxd/docs/master/authentication/) for more information.

### Use a remote simple streams server

To add a simple streams server as a remote, use the following command:

	lxc remote add some-name https://example.com/some/path --protocol=simplestreams

### Use a remote LXD server

To add a LXD server as a remote, use the following command:

	lxc remote add some-name <IP|FQDN|URL> [flags]

Some authentication methods require specific flags (for example, use `lxc remote add some-name <IP|FQDN|URL> --auth-type=candid` for Candid authentication). See [Remote API authentication](/lxd/docs/master/authentication/) for more information.

An example using an IP address:

    lxc remote add remoteserver2 1.2.3.4

This will prompt you to confirm the remote server fingerprint and then ask you for the password or token, depending on the authentication method used by the remote.

### Use remote servers

#### Image list on a remote server
A list of images on that server can be obtained with:

    lxc image list my-images:

#### Launch an instance
Launch an instance based on an image of that server:

    lxc launch some-name:image-name your-instance [--vm]

#### Manage instances on a remote server
You can use the same commands but prefixing the server and instance name like:

    lxc exec remoteserver-name:instancename -- apt-get update

You can replace `apt-get update` with any command the instance supports.
