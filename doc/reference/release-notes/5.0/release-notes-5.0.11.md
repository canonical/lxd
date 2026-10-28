---
myst:
  html_meta:
    description: Release notes for LXD 5.0.11, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-5.0.11)=
# LXD 5.0.11 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 5.0 LTS series. It updates the Go and snap dependencies and raises the minimum Go version.

(ref-release-notes-5.0.11-go)=
## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.27.3.

(ref-release-notes-5.0.11-snap)=
## Snap packaging changes

- AppArmor bumped to 4.0.3
- EDK2 bumped to 2024.02-2ubuntu0.10
- `libseccomp` bumped to 2.5.6
- `libtpms` bumped to 0.9.7
- `libusb` bumped to 1.0.30
- `spice-protocol` bumped to 0.14.5
- `squashfs-tools-ng` bumped to 1.3.2
- `swtpm` bumped to 0.9.1
- Snap now built with Go 1.27

(ref-release-notes-5.0.11-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-5.0.10...lxd-5.0.11).

(ref-release-notes-5.0.11-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-5.0.11).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=5.0/stable`
- **macOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
