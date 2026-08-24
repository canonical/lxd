---
myst:
  html_meta:
    description: Release notes for LXD 4.0.13, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-4.0.13)=
# LXD 4.0.13 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 4.0 LTS series.

(ref-release-notes-4.0.13-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`Instance template path traversal allows arbitrary host file write as root (CVE-2026-66897)`](https://github.com/canonical/lxd/security/advisories/GHSA-q39m-8fx9-42fv)
- [{spellexception}`Fix inverted conditions in NVIDIA environment variable application`](https://github.com/canonical/lxd/commit/f38136df8a0982bb1a3e338bd8c4a7037c16849a)

(ref-release-notes-4.0.13-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-4.0.12...lxd-4.0.13).

(ref-release-notes-4.0.13-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-4.0.13).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=4.0/stable`
- **macOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
