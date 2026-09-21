---
myst:
  html_meta:
    description: Release notes for LXD 4.0.14, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-4.0.14)=
# LXD 4.0.14 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 4.0 LTS series. It focuses on build reliability fixes backported from the main development branch.

(ref-release-notes-4.0.14-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`Avoid pulling Go modules from Bazaar VCS during source builds`](https://github.com/canonical/lxd/pull/19013)

(ref-release-notes-4.0.14-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-4.0.13...lxd-4.0.14).

(ref-release-notes-4.0.14-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-4.0.14).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=4.0/stable`
- **macOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`