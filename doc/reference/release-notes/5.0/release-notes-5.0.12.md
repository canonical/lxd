---
myst:
  html_meta:
    description: Release notes for LXD 5.0.12, including bugfixes and other updates from the LXD project.
---

(ref-release-notes-5.0.12)=
# LXD 5.0.12 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 5.0 LTS series. It includes bug fixes backported from the main development branch and raises the minimum Go version.

(ref-release-notes-5.0.12-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`Backport make deps, run CI with proper LXC version and GH caching`](https://github.com/canonical/lxd/pull/19505)

(ref-release-notes-5.0.12-go)=
## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.27.4.

(ref-release-notes-5.0.12-snap)=
## Snap packaging changes

- Snap rebuilt with the latest upstream Go version, bringing in the latest fixes of upstream Go packages.

(ref-release-notes-5.0.12-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-5.0.11...lxd-5.0.12).

(ref-release-notes-5.0.12-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-5.0.12).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=5.0/stable`
- **macOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
