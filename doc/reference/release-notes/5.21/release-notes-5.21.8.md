---
myst:
  html_meta:
    description: Release notes for LXD 5.21.8, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-5.21.8)=
# LXD 5.21.8 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 5.21 LTS series. It includes several bug fixes backported from the main development branch.

(ref-release-notes-5.21.8-highlights)=
## Highlights

This section highlights new and improved features in this release.

### `devlxd` moved to shared mounts

The `devlxd` socket is now served from the shared mounts (`shmounts`) mechanism instead of its previous dedicated mount path. Existing instances are automatically migrated from the old `devlxd` path to the new one, and a stacked `tmpfs` mount issue affecting shared mounts was fixed as part of this change.

(ref-release-notes-5.21.8-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`lxd/idmap: fix double-close in shift_linux.go`](https://github.com/canonical/lxd/pull/18930)
- [{spellexception}`Fix failed file operation due to deleted forkfile as a result of a race condition`](https://github.com/canonical/lxd/pull/18953)
- [{spellexception}`Use moveMount() when it's available to avoid failed file operations due to a deleted forkfile`](https://github.com/canonical/lxd/pull/18957)
- [{spellexception}`Fix busy zfs preventing rebuild`](https://github.com/canonical/lxd/pull/18952)
- [{spellexception}`Fix LXCFS clean up logic; avoid generating new certs on running instances`](https://github.com/canonical/lxd/pull/18955)
- [{spellexception}`Fix zfs storage leak after shutdown due to lingering forkfile daemon`](https://github.com/canonical/lxd/pull/18948)
- [{spellexception}`Fix stacked tmpfs shmounts; move devlxd to shmounts; migrate old devlxd path to new path`](https://github.com/canonical/lxd/pull/19020)
- [{spellexception}`snapcraft/commands: ensure LXD shuts down before all MicroCeph/MicroOVN units`](https://github.com/canonical/lxd/pull/19049)

(ref-release-notes-5.21.8-snap)=
## Snap packaging changes

- The snap now ensures LXD shuts down before the MicroCeph and MicroOVN units on refresh, avoiding storage and network disruption.
- The bundled ZFS versions were bumped to their latest patch releases (2.2.11, 2.3.9, and 2.4.4).

(ref-release-notes-5.21.8-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-5.21.7...lxd-5.21.8).

(ref-release-notes-5.21.8-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-5.21.8).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=5.21/stable`
- **macOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
