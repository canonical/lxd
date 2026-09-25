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
- [{spellexception}`Arbitrary file write on host via symlink in migration stream (CVE-2026-87799)`](https://github.com/canonical/lxd/security/advisories/GHSA-fmc3-3cpq-6whr)
- [{spellexception}`btrfs optimized backup/migration: unvalidated subvolume path enables root arbitrary file delete/write (RCE ceiling) (CVE-2026-85185)`](https://github.com/canonical/lxd/security/advisories/GHSA-27q7-qwhm-c34p)
- [{spellexception}`Path traversal via btrfs optimized-backup subvolumes[].path enables root file/dir manipulation (CVE-2026-85526)`](https://github.com/canonical/lxd/security/advisories/GHSA-h85r-gjgx-g2rv)
- [{spellexception}`Malicious agent in a VM can escape the target directory of a recursive file pull (CVE-2026-87798)`](https://github.com/canonical/lxd/security/advisories/GHSA-mr8v-hx34-hfvf)
- [{spellexception}`CLI path traversal when exporting an image from a malicious server (CVE-2026-86334)`](https://github.com/canonical/lxd/security/advisories/GHSA-g4cm-f533-78hq)

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