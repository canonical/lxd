---
myst:
  html_meta:
    description: Release notes for LXD 4.0.15, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-4.0.15)=
# LXD 4.0.15 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 4.0 LTS series. It updates dependencies bundled in the snap package.

(ref-release-notes-4.0.15-snap)=
## Snap packaging changes

- ZFS 2.1 bumped to 2.1.16.
- `libseccomp` bumped to 2.5.6.
- `libusb` bumped to 1.0.30.
- Open vSwitch bumped to 3.0.8.
- `spice-server` bumped to 0.15.2.
- SQLite bumped to 3.40.1.

(ref-release-notes-4.0.15-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-4.0.14...lxd-4.0.15).

(ref-release-notes-4.0.15-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-4.0.15).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=4.0/stable`
- **macOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
