---
myst:
  html_meta:
    description: Release notes for LXD 6.10.1, including bugfixes.
---

(ref-release-notes-6.10.1)=
# LXD 6.10.1 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 6 LTS series. It includes several bug fixes backported from the main development branch.

## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`lxd/instance/drivers/qmp: remove limit on QMP response size`](https://github.com/canonical/lxd/pull/19340)
- [{spellexception}`snap: Cherry-pick LXCFS suspension enablement fix`](https://github.com/canonical/lxd/pull/19332)
- [{spellexception}`lxd/auth/oidc: Use ID token claims in callback`](https://github.com/canonical/lxd/pull/19358)
- [{spellexception}`snap/lvm: Store metadata archives and backups under SNAP_COMMON`](https://github.com/canonical/lxd/pull/19303)

## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-6.10.0...lxd-6.10.1).

(ref-release-notes-6.10.1-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-6.10.1).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=6/stable`
- **macOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
