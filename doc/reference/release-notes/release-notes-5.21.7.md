---
myst:
  html_meta:
    description: Release notes for LXD 5.21.7, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-5.21.7)=
# LXD 5.21.7 release notes

This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
```

This is a maintenance release for the 5.21 LTS series. It includes documentation improvements and dependency updates.

(ref-release-notes-5.21.7-highlights)=
## Highlights

This section highlights new and improved features in this release.

### Enhanced documentation tooling with LLM support

An automated Sphinx extension (`sphinx-llm`) has been added to generate `llms.txt` and `llms-full.txt` files from the documentation. These files make it easier for Large Language Models to work with the LXD documentation.

- Documentation: [sphinx-llm](https://sphinx-llms-txt.readthedocs.io/)

(ref-release-notes-5.21.7-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

- [{spellexception}`Prevent a panic when creating a custom volume snapshot without an expiry date`](https://github.com/canonical/lxd/commit/625e859869a14642fcc33534ae9c4372aa125909)

(ref-release-notes-5.21.7-snap)=
## Snap packaging changes

- QEMU was bumped to version 8.2.2+ds-0ubuntu1.18.
- NVIDIA Container and NVIDIA Container Toolkit were bumped to version 1.20.0.
- EDK2 Secure Boot variable generation now uses `python3-virt-firmware` instead of a QEMU virtual machine during snap builds.

(ref-release-notes-5.21.7-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-5.21.6...lxd-5.21.7).

(ref-release-notes-5.21.7-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-5.21.7).

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=5.21/stable`
- **macOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
