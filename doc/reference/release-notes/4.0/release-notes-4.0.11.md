---
myst:
  html_meta:
    description: Release notes for LXD 4.0.11, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-4.0.11)=
# LXD 4.0.11 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

(ref-release-notes-4.0.11-highlights)=
## Highlights

### Ubuntu Pro detection

LXD now detects whether the host system is attached to Ubuntu Pro and advertises this as a feature in the user agent string. This allows LXD to expose Pro-specific capabilities when running on a Pro-attached host.

(ref-release-notes-4.0.11-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [#18228](https://github.com/canonical/lxd/pull/18228) — Fix for [GHSA-r7w7-mmxr-47r9](https://github.com/lxc/incus/security/advisories/GHSA-r7w7-mmxr-47r9) / CVE-2026-40197: Validate all backup config struct slices for nil values to prevent panics when importing backup configs that contain nil slices.
- [#17981](https://github.com/canonical/lxd/pull/17981) — Fix for [GHSA-83xr-5xxr-mh92](https://github.com/lxc/incus/security/advisories/GHSA-83xr-5xxr-mh92) / CVE-2026-33897: Harden the `pongo2` template rendering engine used for cloud-init and other instance templates. Fixes include: blocking dangerous built-in functions, enforcing a recursion limit, capturing panics to prevent crashes, and preventing output leakage from failed template executions.
- [#16924](https://github.com/canonical/lxd/pull/16924) — Fix for [GHSA-56mx-8g9f-5crf](https://github.com/lxc/incus/security/advisories/GHSA-56mx-8g9f-5crf) / CVE-2025-64507: Tighten storage pool volume directory permissions to reduce exposure of instance data on the host.
- [#17631](https://github.com/canonical/lxd/pull/17631) — Fix for [GHSA-8h3p-58qv-8p53](https://github.com/lxc/incus/security/advisories/GHSA-8h3p-58qv-8p53): Prevent potential shell expansion in LXC hook arguments by switching from `strconv.Quote` (double-quoting) to proper single-quoting (`ShellQuote`) when generating LXC configuration.
- [#17954](https://github.com/canonical/lxd/pull/17954) — Improve validation when editing certificates to reject invalid or inconsistent configurations.
- [#17939](https://github.com/canonical/lxd/pull/17939) — Add `raw.apparmor` and `raw.qemu.conf` to the list of forbidden low-level options when low-level configuration is restricted in project limits.
- [#17821](https://github.com/canonical/lxd/pull/17821) — Fail fast when an unsupported compression algorithm is specified for backup or image operations, rather than encountering a confusing error partway through the operation.
- [#15168](https://github.com/canonical/lxd/pull/15168) / [#15165](https://github.com/canonical/lxd/pull/15165) — Fix panics when a backup config is missing or contains invalid data (for example, when a backup created with a newer version of LXD is imported into LXD 4.0).
- [#15109](https://github.com/canonical/lxd/pull/15109) — Fix AppArmor rules for unprivileged containers to allow `devpts`, `procfs`, and `sysfs` mounts following a stricter AppArmor user space utilities update in the core20 snap.

(ref-release-notes-4.0.11-incompatible)=
## Backwards-incompatible changes

These changes are not compatible with older versions of LXD or its clients.

### Minimum system requirement changes

The minimum supported version of some components has changed:
- The minimum required version of Go to build LXD is now 1.18 (see [Updated minimum Go version](#ref-release-notes-4.0.11-go)).

(ref-release-notes-4.0.11-go)=
## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.18.

(ref-release-notes-4.0.11-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-4.0.10...lxd-4.0.11).

(ref-release-notes-4.0.11-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-4.0.11).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=4.0/stable`
- **MacOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
