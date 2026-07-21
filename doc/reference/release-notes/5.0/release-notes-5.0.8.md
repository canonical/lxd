---
myst:
  html_meta:
    description: Release notes for LXD 5.0.8, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-5.0.8)=
# LXD 5.0.8 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 5.0 LTS series. It focuses on updating the bundled UEFI firmware and snap packaging, along with build and tooling changes backported from the main development branch.

(ref-release-notes-5.0.8-highlights)=
## Highlights

This section highlights notable improvements in this release.

### Updated UEFI firmware (EDK2/OVMF)

The bundled EDK2/OVMF firmware used for virtual machines has been refreshed and modernized:

- The EDK2 sources now build from the Ubuntu Noble (`core24`) source package, replacing the previous custom build.
- The firmware was bumped to `2024.02-2ubuntu0.9`, which ships the Microsoft 2023 Secure Boot keys.
- OVMF firmware is now shipped using the `4MB` file names (`OVMF_CODE.4MB.fd`, `OVMF_VARS.4MB.fd`, `OVMF_VARS.4MB.ms.fd`).
- The QEMU driver now detects the installed UEFI firmware for feature checks, always refreshes the `qemu.nvram` symlink, and regenerates the NVRAM when a virtual machine transitions to the new 4MB firmware. This ensures existing virtual machines pick up the updated firmware cleanly on next start.

(ref-release-notes-5.0.8-incompatible)=
## Backwards-incompatible changes

These changes are not compatible with older versions of LXD or its clients.

### Minimum system requirement changes

The minimum supported version of some components has changed:

- The minimum required version of Go to build LXD is now 1.26.5 (see [Updated minimum Go version](#ref-release-notes-5.0.8-go)).

(ref-release-notes-5.0.8-go)=
## Updated minimum Go version

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now 1.26.5 (previously 1.26.4).

(ref-release-notes-5.0.8-snap)=
## Snap packaging changes

- The EDK2/OVMF part now builds from the Ubuntu Noble (`core24`) source package.
- Bumped EDK2/OVMF to `2024.02-2ubuntu0.9`, which includes the Microsoft 2023 Secure Boot keys, and switched to the 4MB CODE firmware layout.
- Synced the EDK2 boot logo with `latest-edge`.
- Dropped the now unused `qemu-ovmf-secureboot` part and the standalone `nasm` part (and its patch).
- Dropped unused EDK2 patches.

(ref-release-notes-5.0.8-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-5.0.7...lxd-5.0.8).

(ref-release-notes-5.0.8-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-5.0.8).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=5.0/stable`
- **MacOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
