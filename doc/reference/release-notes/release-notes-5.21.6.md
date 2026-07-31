---
myst:
  html_meta:
    description: Release notes for LXD 5.21.6, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-5.21.6)=
# LXD 5.21.6 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 5.21 LTS series. It includes security hardening, cluster connectivity improvements, updated build requirements, and bug fixes backported from the main development branch.

(ref-release-notes-5.21.6-highlights)=
## Highlights

This section highlights new and improved features in this release.

### Internal cluster connections bypass configured HTTP proxies

Internal cluster traffic now bypasses configured HTTP proxies for certificate retrieval, cluster join, and image replication between members. This avoids proxy interference for member-to-member operations and improves cluster reliability in proxied environments.

- Documentation: {ref}`clustering`

### Expanded ACME integration test coverage

A new lightweight ACME test server (`mini-acme`) has been added to the integration test suite, and new standalone and clustering ACME test scenarios are now covered.

- Documentation: {ref}`authentication`

(ref-release-notes-5.21.6-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`Arbitrary file read and write via image metadata.yaml symlink (GHSA-j825-cg34-5fr5)`](https://github.com/canonical/lxd/security/advisories/GHSA-j825-cg34-5fr5)
- [{spellexception}`Root command execution via image backup.yaml symlink (GHSA-fv82-v4fj-mm4m)`](https://github.com/canonical/lxd/security/advisories/GHSA-fv82-v4fj-mm4m)
- [{spellexception}`NVIDIA configuration validation bypass for nvidia.driver.capabilities (GHSA-vfh7-q59q-54v2)`](https://github.com/canonical/lxd/security/advisories/GHSA-vfh7-q59q-54v2)
- [{spellexception}`Restricted project bypass for security.idmap.isolated defaults (GHSA-7vp9-3vmp-c5jm)`](https://github.com/canonical/lxd/security/advisories/GHSA-7vp9-3vmp-c5jm)
- [{spellexception}`Project restriction bypass via instance migration config override (GHSA-gcr9-5q6r-w625)`](https://github.com/canonical/lxd/security/advisories/GHSA-gcr9-5q6r-w625)
- [{spellexception}`Cross-project instance copy bypass via config merge timing (GHSA-v989-qw7w-xvg4)`](https://github.com/canonical/lxd/security/advisories/GHSA-v989-qw7w-xvg4)
- [{spellexception}`Storage volume cross-project move and restore bypass project disk limits (GHSA-5h78-p252-989h)`](https://github.com/canonical/lxd/security/advisories/GHSA-5h78-p252-989h)
- [{spellexception}`Cross-project cluster migration bypasses project restrictions (GHSA-v9wr-9r7q-fh4g)`](https://github.com/canonical/lxd/security/advisories/GHSA-v9wr-9r7q-fh4g)
- [{spellexception}`Cross-project instance move bypasses project restrictions (GHSA-5g5r-wh97-qcq2)`](https://github.com/canonical/lxd/security/advisories/GHSA-5g5r-wh97-qcq2)
- [{spellexception}`Fix listing images with --all-projects and shared fingerprints`](https://github.com/canonical/lxd/pull/18668)
- [{spellexception}`Fix bcache device detection in resources`](https://github.com/canonical/lxd/pull/18704)
- [{spellexception}`Fix nil http.Request handling on updateClusterCertificate`](https://github.com/canonical/lxd/pull/18697)
- [{spellexception}`Fix unprotected concurrent write to operation metadata`](https://github.com/canonical/lxd/pull/18717)
- [{spellexception}`Fix infinite loop in network error log writer`](https://github.com/canonical/lxd/pull/18734)
- [{spellexception}`Fix case-insensitive detection of .ISO files for disk devices`](https://github.com/canonical/lxd/pull/18736)
- [{spellexception}`Fix booting Windows VMs in BIOS mode by disabling hv_passthrough`](https://github.com/canonical/lxd/pull/18740)
- [{spellexception}`Fix ceph cluster_name handling for QEMU disks`](https://github.com/canonical/lxd/pull/18760)

(ref-release-notes-5.21.6-incompatible)=
## Backwards-incompatible changes

These changes are not compatible with older versions of LXD or its clients.

### Minimum system requirement changes

The minimum supported version of some components has changed:

- The minimum required version of Go to build LXD is now 1.26.5 (see [Updated minimum Go version](#ref-release-notes-5.21.6-go)).

(ref-release-notes-5.21.6-go)=
## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.26.5 (previously 1.26.4).

(ref-release-notes-5.21.6-snap)=
## Snap packaging changes

- EDK2/OVMF was bumped to `2024.02-2ubuntu0.9` (MS 2023 keys).
- Snap wrappers now detect the AppArmor user namespace restriction.
- QEMU snap build logic now fails early on `amd64` when `/dev/kvm` is unavailable.

(ref-release-notes-5.21.6-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-5.21.5...lxd-5.21.6).

(ref-release-notes-5.21.6-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-5.21.6).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=5.21/stable`
- **MacOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
