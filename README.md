[![LXD](https://linuxcontainers.org/static/img/containers.png)](https://linuxcontainers.org/lxd)

# LXD

<!-- Include start LXD intro -->

LXD is a modern, secure and powerful system container and virtual machine manager.

It provides a unified experience for running and managing full Linux systems inside containers or virtual machines. LXD supplies images for a [wide number of Linux distributions](https://images.linuxcontainers.org) and is built around a very powerful, yet pretty simple, REST API. LXD scales from one instance on a single machine to a cluster in a full data center rack, making it suitable for running workloads both for development and in production.

LXD allows you to easily set up a system that feels like a small private cloud. You can run any type of workload in an efficient way while keeping your resources optimized.

You should consider using LXD if you want to containerize different environments or run virtual machines, or in general run and manage your infrastructure in a cost-effective way.

## Get started

To get a better idea of what LXD is and what it does, you can [try it online](https://linuxcontainers.org/lxd/try-it/)!
This interactive demo guides you through the basic steps of using LXD and shows how to launch and inspect your first instances, update the configuration, work with commands and files inside an instance and create snapshots.

Then, if you want to run LXD locally, take a look at our [getting started guide](https://linuxcontainers.org/lxd/getting-started-cli/) for installation instructions.

- Release announcements: [`https://linuxcontainers.org/lxd/news/`](https://linuxcontainers.org/lxd/news/)
- Release tarballs: [`https://linuxcontainers.org/lxd/downloads/`](https://linuxcontainers.org/lxd/downloads/)
- Documentation: [`https://linuxcontainers.org/lxd/docs/latest/`](https://linuxcontainers.org/lxd/docs/latest/)

<!-- Include end LXD intro -->

## Status

Type                | Service               | Status
---                 | ---                   | ---
CI (client)         | GitHub                | [![Build Status](https://github.com/lxc/lxd/workflows/Client%20build%20and%20unit%20tests/badge.svg)](https://github.com/lxc/lxd/actions)
CI (server)         | Jenkins               | [![Build Status](https://jenkins.linuxcontainers.org/job/lxd-github-commit/badge/icon)](https://jenkins.linuxcontainers.org/job/lxd-github-commit/)
Go documentation    | Godoc                 | [![GoDoc](https://godoc.org/github.com/lxc/lxd/client?status.svg)](https://godoc.org/github.com/lxc/lxd/client)
Static analysis     | GoReport              | [![Go Report Card](https://goreportcard.com/badge/github.com/lxc/lxd)](https://goreportcard.com/report/github.com/lxc/lxd)
Translations        | Weblate               | [![Translation status](https://hosted.weblate.org/widgets/linux-containers/-/svg-badge.svg)](https://hosted.weblate.org/projects/linux-containers/lxd/)
Project status      | CII Best Practices    | [![CII Best Practices](https://bestpractices.coreinfrastructure.org/projects/1086/badge)](https://bestpractices.coreinfrastructure.org/projects/1086)

<!-- Include start installing -->

## Installing LXD from packages

The LXD daemon only works on Linux but the client tool (`lxc`) is available on most platforms.

OS                  | Format                                            | Command
---                 | ---                                               | ---
Linux               | [Snap](https://snapcraft.io/lxd)                  | `snap install lxd`
Windows             | [Chocolatey](https://chocolatey.org/packages/lxc) | `choco install lxc`
macOS               | [Homebrew](https://formulae.brew.sh/formula/lxc)  | `brew install lxc`

More instructions on installing LXD for a wide variety of Linux distributions and operating systems [can be found on our website](https://linuxcontainers.org/lxd/getting-started-cli/).
<!-- Include end installing -->

To install LXD from source, see [Installing LXD](https://linuxcontainers.org/lxd/docs/latest/installing/) in the documentation.

## Security

<!-- Include start security -->

Consider the following aspects to ensure that your LXD installation is secure:

- Keep your operating system up-to-date and install all available security patches.
- Use only supported LXD versions (LTS releases or monthly feature releases).
- Restrict access to the LXD daemon and the remote API.
- Do not use privileged containers unless required. If you use privileged containers, put appropriate security measures in place. See the [LXC security page](https://linuxcontainers.org/lxc/security/) for more information.
- Configure your network interfaces to be secure.
<!-- Include end security -->

See [Security](https://linuxcontainers.org/lxd/docs/latest/security/) for detailed information.

**IMPORTANT:**
<!-- Include start security note -->
Local access to LXD through the Unix socket always grants full access to LXD.
This includes the ability to attach file system paths or devices to any instance as well as tweak the security features on any instance.

Therefore, you should only give such access to users who you'd trust with root access to your system.
<!-- Include end security note -->
<!-- Include start support -->

## Support and community

The following channels are available for you to interact with the LXD community.

### Bug reports

You can file bug reports and feature requests at: [`https://github.com/lxc/lxd/issues/new`](https://github.com/lxc/lxd/issues/new)

### Forum

A discussion forum is available at: [`https://discuss.linuxcontainers.org`](https://discuss.linuxcontainers.org)

### Mailing lists

We use the LXC mailing lists for developer and user discussions. You can
find and subscribe to those at: [`https://lists.linuxcontainers.org`](https://lists.linuxcontainers.org)

### IRC

If you prefer live discussions, you can find us in [`#lxc`](https://web.libera.chat/#lxc) on `irc.libera.chat`. See [Getting started with IRC](https://discuss.linuxcontainers.org/t/getting-started-with-irc/11920) if needed.

### Commercial support

Commercial support for LXD can be obtained through [Canonical Ltd](https://www.canonical.com).

## Documentation

The official documentation is available at: [`https://linuxcontainers.org/lxd/docs/latest/`](https://linuxcontainers.org/lxd/docs/latest/)

You can find additional resources on the [website](https://linuxcontainers.org/lxd/articles), on [YouTube](https://www.youtube.com/channel/UCuP6xPt0WTeZu32CkQPpbvA) and in the [Tutorials section](https://discuss.linuxcontainers.org/c/tutorials/) in the forum.

<!-- Include end support -->

## Contributing

Fixes and new features are greatly appreciated. Make sure to read our [contributing guidelines](CONTRIBUTING.md) first!
