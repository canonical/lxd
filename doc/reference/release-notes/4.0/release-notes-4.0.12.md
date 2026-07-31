---
myst:
  html_meta:
    description: Release notes for LXD 4.0.12, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-4.0.12)=
# LXD 4.0.12 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 4.0 LTS series. It focuses on security hardening, stricter input validation, and bug fixes backported from the main development branch.

(ref-release-notes-4.0.12-highlights)=
## Highlights

This section highlights notable improvements in this release.

### Security hardening for image and backup handling

Several hardening fixes were backported to reduce archive and template-related attack surface:

- Reject non-regular metadata files after unpack.
- Reject unconfined backup metadata.
- Prevent instance templates from escaping the templates directory.
- Validate backup instance and volume names during import.

### NVIDIA configuration validation improvements

Validation for NVIDIA-related instance configuration was tightened and applied consistently at set time and start time. This includes stricter validation for `nvidia.driver.capabilities`, `nvidia.require.cuda`, and `nvidia.require.driver`.

(ref-release-notes-4.0.12-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`Arbitrary file read and write via image metadata.yaml symlink (GHSA-j825-cg34-5fr5)`](https://github.com/canonical/lxd/security/advisories/GHSA-j825-cg34-5fr5)
- [{spellexception}`NVIDIA configuration validation bypass for nvidia.require.* and nvidia.driver.capabilities (GHSA-vfh7-q59q-54v2)`](https://github.com/canonical/lxd/security/advisories/GHSA-vfh7-q59q-54v2)
- [{spellexception}`Restricted project bypass for security.idmap.isolated defaults (GHSA-7vp9-3vmp-c5jm)`](https://github.com/canonical/lxd/security/advisories/GHSA-7vp9-3vmp-c5jm)
- [{spellexception}`Unconfined backup.yaml accepted after unpack in crafted backups (GHSA-fv82-v4fj-mm4m)`](https://github.com/canonical/lxd/security/advisories/GHSA-fv82-v4fj-mm4m)
- [{spellexception}`Prevent image metadata templates from escaping the instance directory (GHSA-9hcm-hxh5-7xxh)`](https://github.com/canonical/lxd/security/advisories/GHSA-9hcm-hxh5-7xxh)
- [{spellexception}`Validate backup import names to prevent path traversal during restore (GHSA-m857-c7gc-c984)`](https://github.com/canonical/lxd/security/advisories/GHSA-m857-c7gc-c984)

(ref-release-notes-4.0.12-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-4.0.11...lxd-4.0.12).

(ref-release-notes-4.0.12-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-4.0.12).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=4.0/stable`
- **MacOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
