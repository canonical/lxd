---
myst:
  html_meta:
    description: Release notes for LXD 5.0.7, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-5.0.7)=
# LXD 5.0.7 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 5.0 LTS series. It focuses on security hardening, stricter input validation, and bug fixes backported from the main development branch.

(ref-release-notes-5.0.7-highlights)=
## Highlights

This section highlights notable improvements in this release.

### Security hardening

A number of inputs are now validated more strictly and some low-level options have been further restricted as part of security hardening backports:

- Stricter image fingerprint validation, including computing and verifying the combined hash during image downloads.
- Stricter validation of low-level VM options: `raw.apparmor` and `raw.qemu.conf` were added to the list of forbidden low-level options.
- Improved validation when editing certificates.
- Validation of struct slices and configuration during backup import.
- Tightened the compression algorithm validation to only allow supported values.

### Template rendering hardening

The instance template rendering logic was reworked to align with the standard `RenderTemplate` implementation. This introduces a recursion limit, blocks some `pongo2` template functions, handles panics from the `pongo2` package, and avoids leaking output from failed template execution.

(ref-release-notes-5.0.7-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`Compute and verify combined hash during image downloads`](https://github.com/canonical/lxd/pull/17994)
- [{spellexception}`Validate all backup config slices for nil values`](https://github.com/canonical/lxd/pull/18227)
- [{spellexception}`Validate backup .Config`](https://github.com/canonical/lxd/pull/18227)
- [{spellexception}`Do not persist changes in UpdateInstanceConfig during import`](https://github.com/canonical/lxd/pull/17968)
- [{spellexception}`Do not use backup config from disk in internalImportFromBackup`](https://github.com/canonical/lxd/pull/17968)
- [{spellexception}`Always exclude backup config from the generic VFS tarball`](https://github.com/canonical/lxd/pull/17968)
- [{spellexception}`Validate whether instance snapshot can be created in createFromBackup`](https://github.com/canonical/lxd/pull/18304)
- [{spellexception}`Quote architecture name supplied in shared/osarch`](https://github.com/canonical/lxd/pull/18304)
- [{spellexception}`Do not bypass the instance limit check`](https://github.com/canonical/lxd/pull/17991)
- [{spellexception}`Fail fast if the compression algorithm is unsupported for images, instance backups, and storage volume backups`](https://github.com/canonical/lxd/pull/17820)
- [{spellexception}`Capture panics from pongo2 during template rendering`](https://github.com/canonical/lxd/pull/17980)
- [{spellexception}`Introduce a recursion limit to RenderTemplate()`](https://github.com/canonical/lxd/pull/17980)
- [{spellexception}`Block some pongo2 functions in templates`](https://github.com/canonical/lxd/pull/17980)

(ref-release-notes-5.0.7-incompatible)=
## Backwards-incompatible changes

These changes are not compatible with older versions of LXD or its clients.

### Minimum system requirement changes

The minimum supported version of some components has changed:

- The minimum required version of Go to build LXD is now 1.25.8 (see [Updated minimum Go version](#ref-release-notes-5.0.7-go)).

### Stricter validation and tightened permissions

Several inputs are now validated more strictly, and some low-level options have been further restricted as part of security hardening backports. Requests that previously succeeded with malformed or unexpected values may now be rejected:

- Stricter image fingerprint validation.
- Stricter checks for low-level (`raw.*`) VM configuration options, including `raw.apparmor` and `raw.qemu.conf`.
- Improved certificate edit validation.
- Validation of struct slices and configuration during import.
- The compression algorithm validation now only allows supported values.

(ref-release-notes-5.0.7-go)=
## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.25.8 (previously 1.24.6).

(ref-release-notes-5.0.7-snap)=
## Snap packaging changes

- Transitioned the snap base from `core20` to `core22`.
- QEMU is now built from the Ubuntu source package (`8.2.2+ds-0ubuntu1.17`) instead of upstream Git.
- Added an edk2/OVMF patch to disable the UEFI shell when Secure Boot is enabled.
- Added the `apparmor.unprivileged-restrictions-disable` snap configuration option (default `true`).
- The `lxd-ui` build now uses Node.js 20 (previously 18).
- Refreshed the bundled Ceph stage libraries for `core22`.

(ref-release-notes-5.0.7-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-5.0.6...lxd-5.0.7).

(ref-release-notes-5.0.7-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-5.0.7).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=5.0/stable`
- **MacOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
