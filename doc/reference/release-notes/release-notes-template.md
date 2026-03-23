---
myst:
  html_meta:
    description: Release notes for LXD x.y, including highlights about new features, bugfixes, and other updates from the LXD project.
---

(ref-release-notes-x.y)=
# LXD x.y release notes

% NOTE: This is a basic template for LXD release notes. It is excluded from the Sphinx build of the documentation site. To use it, find and replace `x.y` with the new release version. Follow any instructions in comments, then delete the comment. See existing release notes for examples.

% Remove the comment prefix (%) from one of the two sentences below, and delete the other.
% This is a {ref}`feature release <ref-releases-feature>` and is not recommended for production use.
% This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.

% Add the Discourse link in the admonition below, or delete the sentence with the link if there are no LXD UI updates in this release.
```{admonition} Release notes content
:class: note
These release notes cover updates in the [core LXD repository](https://github.com/canonical/lxd) and the [LXD snap package](https://snapcraft.io/lxd).
For a tour of [LXD UI](https://github.com/canonical/lxd-ui) updates, please see the release announcement in [our Discourse forum]().
```

% Optionally add any other introductory notes here. Fill out the content of the sections below, and delete any sections that are not relevant to this release.

(ref-release-notes-x.y-highlights)=
## Highlights

This section highlights new and improved features in this release.

### Feature short description placeholder (replace this text)

% Dedicate a separate ### section for each new and improved feature highlight description.

(ref-release-notes-x.y-ui)=
## UI updates

% Dedicate a separate ### section for each new and improved UI update description.

(ref-release-notes-x.y-bugfixes)=
## Bug fixes

The following bug fixes are included in this release.

% List of links to resolved bugfix issues

(ref-release-notes-x.y-incompatible)=
## Backwards-incompatible changes

These changes are not compatible with older versions of LXD or its clients.

### Minimum system requirement changes

The minimum supported version of some components has changed:

% List of any updated minimum versions

### Incompatible change short description placeholder (replace this text)

% Dedicate a separate ### section to describe each change that is not backwards compatible.

(ref-release-notes-x.y-deprecated)=
## Deprecated features

These features are removed in this release.

### Deprecated feature short description placeholder (replace this text)

% Dedicate a separate ### section to describe each deprecated feature.

(ref-release-notes-x.y-go)=
## Updated minimum Go version

% Update the minimum version of Go, or delete this entire section if the minimum version has not changed.

If you are building LXD from source instead of using a package manager, the minimum version of Go required to build LXD is now ?.?.?.

(ref-release-notes-x.y-snap)=
## Snap packaging changes

% List of any snap packaging changes

(ref-release-notes-x.y-changelog)=
## Change log

View the [complete list of all changes in this release](https://github.com/canonical/lxd/compare/lxd-a.b...lxd-x.y).

(ref-release-notes-x.y-downloads)=
## Downloads

The source tarballs and binary clients can be found on our [download page](https://github.com/canonical/lxd/releases/tag/lxd-x.y).

% Update the lxd track `x` in the snap command below.

Binary packages are also available for:

- **Linux:** `snap install lxd --channel=x/stable`
- **MacOS client:** `brew install lxc`
- **Windows client:** `choco install lxc`
