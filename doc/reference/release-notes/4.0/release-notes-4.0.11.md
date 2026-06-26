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

- [{spellexception}`Arbitrary file write on host via `exec-output` symlink in crafted image (CVE-2026-48750)`](https://github.com/canonical/lxd/security/advisories/GHSA-9j25-mm2h-2f76)
- [{spellexception}`Arbitrary file read+write on host via templates/ symlink in malicious image (CVE-2026-48752)`](https://github.com/canonical/lxd/security/advisories/GHSA-jpf8-86f3-wp38)
- [{spellexception}`Arbitrary file read+write on host via rootfs/ symlink in malicious image (CVE-2026-48749)`](https://github.com/canonical/lxd/security/advisories/GHSA-vghh-5rfx-xhq8)
- [{spellexception}`Argument injection in backup compression algorithm leading to AFW and ACE (CVE-2026-48755)`](https://github.com/canonical/lxd/security/advisories/GHSA-fmc8-p6q7-75cc)
- [{spellexception}`Arbitrary file write on client due to trusted image hash (CVE-2026-48769)`](https://github.com/canonical/lxd/security/advisories/GHSA-pjff-c2wc-f6jm)
- [{spellexception}`Panic when importing backup configs that contain nil slices (CVE-2026-40197)`](https://github.com/lxc/incus/security/advisories/GHSA-r7w7-mmxr-47r9)
- [{spellexception}`Template sandbox escapes and crash risks in pongo2 rendering (CVE-2026-33897)`](https://github.com/lxc/incus/security/advisories/GHSA-83xr-5xxr-mh92)
- [{spellexception}`Overly permissive storage pool volume directory permissions expose instance data (CVE-2025-64507)`](https://github.com/lxc/incus/security/advisories/GHSA-56mx-8g9f-5crf)
- [{spellexception}`Potential shell expansion in LXC hook arguments due to incorrect quoting`](https://github.com/lxc/incus/security/advisories/GHSA-8h3p-58qv-8p53)
- [{spellexception}`Improve validation when editing certificates to reject invalid or inconsistent configurations`](https://github.com/canonical/lxd/pull/17954)
- [{spellexception}`Add raw.apparmor and raw.qemu.conf to the list of forbidden low-level options when low-level configuration is restricted in project limits`](https://github.com/canonical/lxd/pull/17939)
- [{spellexception}`Fail fast when an unsupported compression algorithm is specified for backup or image operations`](https://github.com/canonical/lxd/pull/17821)
- [{spellexception}`Fix panics when importing backups with missing or invalid configuration data`](https://github.com/canonical/lxd/pull/15168)
- [{spellexception}`Fix AppArmor rules for unprivileged containers to allow devpts, procfs, and sysfs mounts`](https://github.com/canonical/lxd/pull/15109)

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
