---
relatedlinks: "[Snap&#32;documentation](https://snapcraft.io/docs),[LXD&#32;snap&#32;package](https://snapcraft.io/lxd)"
---

(ref-releases-snap)=
# Releases and snap

(ref-releases)=
## Releases

The LXD team maintains both Long Term Support (LTS) and feature releases in parallel. Release notes are published on [Discourse](https://discourse.ubuntu.com/tags/c/lxd/news/143/release).

(ref-releases-lts)=
### LTS releases

LTS releases are **intended for production use**.

LXD follows the [Ubuntu release cycle](https://ubuntu.com/about/release-cycle) cadence, meaning that an LTS release of LXD is created every two years. The release names follow the format _x.y.z_, always including the point number _z_. Updates are provided through point releases, incrementing _z_.

(ref-releases-lts-support)=
#### Support

LTS releases receive standard support for five years, meaning that it receives continuous updates according to the support levels described below. An [Ubuntu Pro](https://ubuntu.com/pro) subscription can provide additional support and extends the support duration by an additional five years.

(ref-releases-lts-support-levels)=
#### Support levels

Standard support for an LTS release starts at full support for its first two years, then moves to maintenance support for the remaining three years. Once an LTS reaches End of Life (EOL), it no longer receives any updates.

- **Full support**: Some new features, frequent bugfixes, and security updates are provided every six months. This schedule is an estimate that can change based on priorities and discovered bugs.
- **Maintenance support**: High impact bugfixes and critical security updates are provided as needed.

(ref-releases-lts-support-current)=
#### Currently supported

The currently supported LTS releases are 5.21._z_ and 5.0._z_.

- 5.21._z_ is supported until June 2029.
  - Currently in full support phase.
- 5.0._z_ is supported until June 2027.
  - Currently in maintenance support phase.

(ref-releases-feature)=
### Feature releases

Feature releases are pushed out more often and contain the newest features and bugfixes. Since they are less tested than LTS releases, they are **not recommended for production use**.

These releases follow the format _x.y_, and they never include a point number _z_. Currently, feature releases for LXD are numbered {{current_feature_track}}._y_, with _y_ incrementing for each new release. Every two years, the latest feature release becomes an LTS release.

#### Support

Feature releases receive continuous updates via each new release. The newest release at any given time is also eligible for additional support through an [Ubuntu Pro](https://ubuntu.com/pro) subscription.

(ref-snap)=
## The LXD snap

The recommended way to {ref}`install LXD <installing>` is [its snap package](https://snapcraft.io/lxd), if snaps are available for your system. A key benefit of snap packaging is that it includes all required dependencies. This allows LXD to run in a consistent environment on many different Linux distributions. Using the snap also streamlines updates through its [channels](https://snapcraft.io/docs/channels).

(ref-snap-channels)=
### Channels

Each installed LXD snap follows a [channel](https://snapcraft.io/docs/channels). Channels are composed of a {ref}`track <ref-snap-tracks>` and a {ref}`risk level <ref-snap-risk>` (for example, the {{current_feature_track}}/stable channel). Each channel points to one release at a time, and when a new release is published to a channel, it replaces the previous one. {ref}`Updating the snap <howto-snap-updates>` then updates to that release.

To view all available channels, run:

```bash
snap info lxd
```

(ref-snap-tracks)=
### Tracks

LXD releases are grouped under [snap tracks](https://snapcraft.io/docs/channels#heading--tracks), such as {{current_feature_track}} or {{current_lts_track}}.

(ref-snap-tracks-lts)=
#### LTS tracks

LXD LTS tracks use the format _x[.y]_, corresponding to the major and minor numbers of {ref}`ref-releases-lts`.

Tracks up to `5.21` include both _x_ and _y_, but future LTS tracks will use only _x_.

(ref-snap-track-feature)=
#### Feature track

The LXD feature track uses the major number of the current {ref}`feature release <ref-releases-feature>`. The current feature track is {{current_feature_track}}.

Feature releases within the same major version are published to the same track, replacing the previous release. For example, the `6.4` release replaced `6.3` in the `6` track. This simplifies updates, as you don't need to switch channels to access new feature releases within the same major version.

Every two years, the current feature track becomes the next LTS, and a new feature track is then created by incrementing _x_. For example, after the `6` track becomes an LTS, the `7` track is created and becomes the next feature track.

(ref-snap-tracks-default)=
#### The default track

If you {ref}`install the LXD snap <installing-snap-package>` without specifying a track, the recommended default is used. The default track always points to the most recent LTS track, which is currently {{current_lts_track}}.

(ref-snap-tracks-latest)=
#### The `latest` track

In the list of channels shown by `snap info lxd`, you might see channels with a track named `latest`. This track typically points to the latest feature release.

Since `latest` is a continuously rolling release track, it might become incompatible with your host OS version over time. Due to this, this track is _not recommended for general use_ and might be removed in the future. Instead, use a feature or LTS track.

(ref-snap-risk)=
### Risk levels

For each LXD track, there are three [risk levels](https://snapcraft.io/docs/channels#heading--risk-levels): `stable`, `candidate`, and `edge`.

We recommend that you use the `stable` risk level to install fully tested releases; this is the only risk level supported under [Ubuntu Pro](https://ubuntu.com/pro), as well as the default risk level if one is not specified at install. The `candidate` and `edge` levels offer newer but less-tested updates, posing higher risk.

(ref-snap-updates)=
### Updates

By default, installed snaps update automatically when new releases are published to the channel they're tracking. For control over LXD updates, we recommend that you modify this auto-update behavior by either {ref}`holding <howto-snap-updates-hold>` or {ref}`scheduling updates <howto-snap-updates-schedule>` as described in our {ref}`howto-snap` guide. You can then apply updates according to your needs.

(ref-snap-updates-cluster)=
#### Updates on clusters

New LXD releases are [published progressively as snaps](https://documentation.ubuntu.com/snapcraft/stable/how-to/publishing/manage-revisions-and-releases/#deliver-a-progressive-release). This means that updates might not be immediately available to all machines at the same time.

This can cause issues when updating the LXD snap for {ref}`clusters <exp-clusters>`, as cluster members must use the same version of the snap at all times. For a guide on how to avoid this issue using the `--cohort` flag, see {ref}`howto-snap-updates-sync`.

(ref-snap-upgrades-downgrades)=
### Upgrades, downgrades, and reverting

To upgrade LXD means to move to a newer {ref}`track <ref-snap-tracks>`, such as from {{current_lts_track}} to {{current_feature_track}}. For instructions, see: {ref}`howto-snap-change`.

Downgrading (moving to a channel with an older track, such as from {{current_feature_track}} to {{current_lts_track}}) is neither recommended nor supported, as breaking changes can exist between major versions.

Breaking changes can be introduced between feature release versions as well. Thus, we also do not support reverting to an older feature release revision.

## Related topics

How-to guides:

- {ref}`support`
- {ref}`installing-snap-package`
- {ref}`howto-snap`
