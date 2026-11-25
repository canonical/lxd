---
myst:
  html_meta:
    description: Release notes for LXD 6.10.2, including bugfixes.
---

(ref-release-notes-6.10.2)=
# LXD 6.10.2 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 6 LTS series. It includes several bug fixes backported from the main development branch.

## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`snap: bump parts from resolute-updates (QEMU and EDK2 updates)`](https://github.com/canonical/lxd/pull/19380)
- [{spellexception}`Confine swtpm with AppArmor`](https://github.com/canonical/lxd/pull/19396)
- [{spellexception}`Network: Allow OVN network updates when uplink has no IPv6`](https://github.com/canonical/lxd/pull/19454)
- [{spellexception}`lxd/instance/drivers/lxc: Map linux.sysctl.* to lxc.sysctl.*`](https://github.com/canonical/lxd/pull/19463)
- [{spellexception}`lxd/instance: Re-allocate ID map when rebuilding unstarted instances`](https://github.com/canonical/lxd/pull/19492)

(ref-release-notes-6.10.2-go)=
## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.27.4.

(ref-release-notes-6.10.2-snap)=
## Snap packaging changes

- QEMU was updated to `10.2.1+ds-1ubuntu3.3` and EDK2 to `2025.11-3ubuntu7.3`, along with other parts bumped from `resolute-updates`.
- Snap rebuilt with the latest upstream Go version, bringing in the latest fixes of upstream Go packages.

## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-6.10.1...lxd-6.10.2).

(ref-release-notes-6.10.2-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-6.10.2).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=6/stable`
- **macOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
