---
myst:
  html_meta:
    description: Release notes for LXD 5.21.10, including bugfixes and other updates from the LXD project.
---

(ref-release-notes-5.21.10)=
# LXD 5.21.10 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 5.21 LTS series. It includes several bug fixes backported from the main development branch, and updates the Go module dependencies.

(ref-release-notes-5.21.10-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`lxd/instance/drivers/lxc: Map linux.sysctl.* to lxc.sysctl.*`](https://github.com/canonical/lxd/pull/19464)
- [{spellexception}`lxd/instance: Re-allocate ID map when rebuilding unstarted instances`](https://github.com/canonical/lxd/pull/19493)
- [{spellexception}`Backport s390x multiple vCPU support`](https://github.com/canonical/lxd/pull/19508)

(ref-release-notes-5.21.10-go)=
## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.27.4.

(ref-release-notes-5.21.10-snap)=
## Snap packaging changes

- Snap rebuilt with the latest upstream Go version, bringing in the latest fixes of upstream Go packages.

(ref-release-notes-5.21.10-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-5.21.9...lxd-5.21.10).

(ref-release-notes-5.21.10-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-5.21.10).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=5.21/stable`
- **macOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
