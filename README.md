# LXD

LXD is a modern, secure and powerful system container and virtual machine manager.

<!-- Include start LXD intro -->

It provides a unified experience for running and managing full Linux systems inside containers or virtual machines. LXD supports images for a large number of Linux distributions (official Ubuntu images and images provided by the community) and is built around a very powerful, yet pretty simple, REST API. LXD scales from one instance on a single machine to a cluster in a full data center rack, making it suitable for running workloads both for development and in production.

LXD allows you to easily set up a system that feels like a small private cloud. You can run any type of workload in an efficient way while keeping your resources optimized.

You should consider using LXD if you want to containerize different environments or run virtual machines, or in general run and manage your infrastructure in a cost-effective way.


<!-- Include end LXD intro -->

## Get started

See [Getting started](https://documentation.ubuntu.com/lxd/en/latest/getting_started/) in the LXD documentation for installation instructions and first steps.

- Release announcements: [`https://discourse.ubuntu.com/c/lxd/news/`](https://discourse.ubuntu.com/c/lxd/news/)
- Release tarballs: [`https://github.com/canonical/lxd/releases/`](https://github.com/canonical/lxd/releases/)
- Documentation: [`https://documentation.ubuntu.com/lxd/en/latest/`](https://documentation.ubuntu.com/lxd/en/latest/)


## Status

Type                | Service               | Status
---                 | ---                   | ---
Tests               | GitHub                | [![Build Status](https://github.com/canonical/lxd/actions/workflows/tests.yml/badge.svg?branch=main)](https://github.com/canonical/lxd/actions?query=event%3Apush+branch%3Amain)
Go documentation    | Godoc                 | [![GoDoc](https://godoc.org/github.com/canonical/lxd/client?status.svg)](https://godoc.org/github.com/canonical/lxd/client)
Static analysis     | GoReport              | [![Go Report Card](https://goreportcard.com/badge/github.com/canonical/lxd)](https://goreportcard.com/report/github.com/canonical/lxd)
Translations        | Weblate               | [![Translation status](https://hosted.weblate.org/widgets/linux-containers/-/svg-badge.svg)](https://hosted.weblate.org/projects/linux-containers/lxd/)

## Installing LXD from packages

The LXD daemon only works on Linux but the client tool (`lxc`) is available on most platforms.

OS                  | Format                                            | Command
---                 | ---                                               | ---
Linux               | [Snap](https://snapcraft.io/lxd)                  | `snap install lxd`
Windows             | [Chocolatey](https://chocolatey.org/packages/lxc) | `choco install lxc`
macOS               | [Homebrew](https://formulae.brew.sh/formula/lxc)  | `brew install lxc`

For more instructions on installing LXD for a wide variety of Linux distributions and operating systems, and to install LXD from source, see [How to install LXD](https://documentation.ubuntu.com/lxd/en/latest/installing/) in the documentation.

## Security

<!-- Include start security -->

Consider the following aspects to ensure that your LXD installation is secure:

- Keep your operating system up-to-date and install all available security patches.
- Use only supported LXD versions (LTS releases or monthly feature releases).
- Restrict access to the LXD daemon and the remote API.
- Do not use privileged containers unless required. If you use privileged containers, put appropriate security measures in place. See the [LXD security page](https://documentation.ubuntu.com/lxd/en/latest/explanation/security/) for more information.
- Configure your network interfaces to be secure.
<!-- Include end security -->

See [Security](https://documentation.ubuntu.com/lxd/en/latest/explanation/security/) for detailed information.

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

You can file bug reports and feature requests at: [`https://github.com/canonical/lxd/issues/new`](https://github.com/canonical/lxd/issues/new)

### Forum

A discussion forum is available at: [`https://discourse.ubuntu.com/c/lxd/`](https://discourse.ubuntu.com/c/lxd/)

### IRC

If you prefer live discussions, you can find us in [`#lxd`](https://web.libera.chat/#lxd) on `irc.libera.chat`. See [Getting started with IRC](https://discourse.ubuntu.com/t/getting-started-with-irc/37907) if needed.

### Commercial support

Commercial support for LXD can be obtained through [Canonical Ltd](https://www.canonical.com).

## Documentation

The official documentation is available at: [`https://documentation.ubuntu.com/lxd/en/latest/`](https://documentation.ubuntu.com/lxd/en/latest/)

You can find additional resources on the [website](https://ubuntu.com/lxd), on [YouTube](https://www.youtube.com/channel/UCuP6xPt0WTeZu32CkQPpbvA) and in the [Tutorials section](https://discourse.ubuntu.com/c/lxd/tutorials/) in the forum.

<!-- Include end support -->

## Contributing

Fixes and new features are greatly appreciated. Make sure to read our [contributing guidelines](CONTRIBUTING.md) first!
