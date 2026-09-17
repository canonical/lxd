---
myst:
  html_meta:
    description: Release notes for LXD 5.0.10, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-5.0.10)=
# LXD 5.0.10 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 5.0 LTS series. It includes storage and `devlxd` reliability fixes and other bug fixes backported from the main development branch.

(ref-release-notes-5.0.10-highlights)=
## Highlights

This section highlights notable improvements in this release.

### `devlxd` moved to shared mounts

The `devlxd` socket is now served from the shared mounts (`shmounts`) mechanism instead of its previous dedicated mount path. Existing instances are automatically migrated from the old `devlxd` path to the new one, and a stacked `tmpfs` mount issue affecting shared mounts was fixed as part of this change.

(ref-release-notes-5.0.10-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`ZFS storage pool leaked after shutdown due to a lingering forkfile daemon`](https://github.com/canonical/lxd/pull/18946)
- [{spellexception}`File operations failed due to a deleted forkfile as a result of a race condition`](https://github.com/canonical/lxd/pull/18954)
- [{spellexception}`LXCFS clean up logic could avoid generating new certificates on running instances`](https://github.com/canonical/lxd/pull/18956)
- [{spellexception}`Fixed stacked tmpfs shmounts and migrated the devlxd path to shared mounts`](https://github.com/canonical/lxd/pull/19021)

(ref-release-notes-5.0.10-snap)=
## Snap packaging changes

- ZFS 2.2 bumped to 2.2.11.

(ref-release-notes-5.0.10-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-5.0.9...lxd-5.0.10).

(ref-release-notes-5.0.10-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-5.0.10).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=5.0/stable`
- **macOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
