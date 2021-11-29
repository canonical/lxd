[![LXD](https://linuxcontainers.org/static/img/containers.png)](https://linuxcontainers.org/lxd)
# LXD
LXD is a next generation system container and virtual machine manager.  
It offers a unified user experience around full Linux systems running inside containers or virtual machines.

It's image based with pre-made images available for a [wide number of Linux distributions](https://images.linuxcontainers.org)  
and is built around a very powerful, yet pretty simple, REST API.

To get a better idea of what LXD is and what it does, you can [try it online](https://linuxcontainers.org/lxd/try-it/)!  
Then if you want to run it locally, take a look at our [getting started guide](https://linuxcontainers.org/lxd/getting-started-cli/).

Release announcements can be found here: <https://linuxcontainers.org/lxd/news/>  
And the release tarballs here: <https://linuxcontainers.org/lxd/downloads/>
The documentation is here: <https://linuxcontainers.org/lxd/docs/master/>

## Status
Type                | Service               | Status
---                 | ---                   | ---
CI (client)         | GitHub                | [![Build Status](https://github.com/lxc/lxd/workflows/Client%20build%20and%20unit%20tests/badge.svg)](https://github.com/lxc/lxd/actions)
CI (server)         | Jenkins               | [![Build Status](https://jenkins.linuxcontainers.org/job/lxd-github-commit/badge/icon)](https://jenkins.linuxcontainers.org/job/lxd-github-commit/)
Go documentation    | Godoc                 | [![GoDoc](https://godoc.org/github.com/lxc/lxd/client?status.svg)](https://godoc.org/github.com/lxc/lxd/client)
Static analysis     | GoReport              | [![Go Report Card](https://goreportcard.com/badge/github.com/lxc/lxd)](https://goreportcard.com/report/github.com/lxc/lxd)
Translations        | Weblate               | [![Translation status](https://hosted.weblate.org/widgets/linux-containers/-/svg-badge.svg)](https://hosted.weblate.org/projects/linux-containers/lxd/)
Project status      | CII Best Practices    | [![CII Best Practices](https://bestpractices.coreinfrastructure.org/projects/1086/badge)](https://bestpractices.coreinfrastructure.org/projects/1086)

## Installing LXD from packages
The LXD daemon only works on Linux but the client tool (`lxc`) is available on most platforms.

OS                  | Format                                            | Command
---                 | ---                                               | ---
Linux               | [Snap](https://snapcraft.io/lxd)                  | snap install lxd
Windows             | [Chocolatey](https://chocolatey.org/packages/lxc) | choco install lxc
MacOS               | [Homebrew](https://formulae.brew.sh/formula/lxc)  | brew install lxc

More instructions on installing LXD for a wide variety of Linux distributions and operating systems [can be found on our website](https://linuxcontainers.org/lxd/getting-started-cli/).

To install LXD from source, see [Installing LXD](installing.md) in the documentation.

## Security
LXD, similar to other container and VM managers provides a UNIX socket for local communication.

**WARNING**: Anyone with access to that socket can fully control LXD, which includes
the ability to attach host devices and filesystems, this should
therefore only be given to users who would be trusted with root access
to the host.

When listening on the network, the same API is available on a TLS socket
(HTTPS), specific access on the remote API can be restricted through
Canonical RBAC.


More details are [available here](security.md).

## Getting started with LXD
Now that you have LXD running on your system you can read the [getting started guide](https://linuxcontainers.org/lxd/getting-started-cli/) or go through more examples and configurations in [our documentation](https://linuxcontainers.org/lxd/docs/master/).

## Bug reports
Bug reports and Feature requests can be filed at: <https://github.com/lxc/lxd/issues/new>

## Contributing
Fixes and new features are greatly appreciated but please read our [contributing guidelines](contributing.md) first.

## Support and discussions
### Forum
A discussion forum is available at: <https://discuss.linuxcontainers.org>

### Mailing-lists
We use the LXC mailing-lists for developer and user discussions, you can
find and subscribe to those at: <https://lists.linuxcontainers.org>

### IRC
If you prefer live discussions, you can find us in [#lxc](https://kiwiirc.com/client/irc.libera.chat/#lxc) on irc.libera.chat.

```{toctree}
:hidden:
:titlesonly:

self
getting_started
configuration
images
operation
restapi_landing
internals
external_resources
```
