---
myst:
  html_meta:
    description: Release notes for LXD 5.0.9, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-5.0.9)=
# LXD 5.0.9 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 5.0 LTS series. It includes QEMU, NVIDIA package and dependency updates.

(ref-release-notes-5.0.9-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`Instance template path traversal allows arbitrary host file write as root (CVE-2026-66897)`](https://github.com/canonical/lxd/security/advisories/GHSA-q39m-8fx9-42fv)
- [{spellexception}`NVIDIA requirement expressions were not applied when configured`](https://github.com/canonical/lxd/commit/887172b10ee3092e768a47b0caabd9514c2373af)

(ref-release-notes-5.0.9-snap)=
## Snap packaging changes

- QEMU was bumped to version 8.2.2+ds-0ubuntu1.18.
- NVIDIA Container was bumped to version 1.20.0.

(ref-release-notes-5.0.9-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-5.0.8...lxd-5.0.9).

(ref-release-notes-5.0.9-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-5.0.9).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=5.0/stable`
- **macOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
