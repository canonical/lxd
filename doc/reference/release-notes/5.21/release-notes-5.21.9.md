---
myst:
  html_meta:
    description: Release notes for LXD 5.21.9, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-5.21.9)=
# LXD 5.21.9 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 5.21 LTS series. It includes a bug fix, and updates the Go module dependencies and the components bundled in the snap.

(ref-release-notes-5.21.9-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`lxd/storage/drivers/btrfs_volumes: ignore failure to clear former exclusive data limit in SetVolumeQuota`](https://github.com/canonical/lxd/pull/19418)

(ref-release-notes-5.21.9-go)=
## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.27.3.

(ref-release-notes-5.21.9-snap)=
## Snap packaging changes

- EDK2 bumped to 2024.02-2ubuntu0.10
- `nvidia-container` bumped to v1.20.1
- `nvidia-container-toolkit` bumped to v1.20.1

(ref-release-notes-5.21.9-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-5.21.8...lxd-5.21.9).

(ref-release-notes-5.21.9-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-5.21.9).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=5.21/stable`
- **macOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
