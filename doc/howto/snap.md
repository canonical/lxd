---
discourse: "[Discourse&#x3a&#32;Managing&#32;the&#32;LXD&#32;snap&#32;package](37214)"
---

(howto-snap)=
# How to manage the LXD snap

The recommended way to manage LXD is its [snap package](https://snapcraft.io/lxd).

For the installation guide, see: {ref}`installing-snap-package`. For details about the LXD snap, including its {ref}`channels <ref-snap-channels>`, {ref}`tracks <ref-snap-tracks>`, and {ref}`release processes <ref-releases>`, see: {ref}`ref-releases-snap`.

(howto-snap-info)=
## View snap information

To view information about the LXD snap, including the available channels and installed version, run:

```bash
snap info lxd
```

To view information about the installed version only, run:

```bash
snap list lxd
```

Sample output:

```{terminal}
:input: snap list lxd
:user: root
:host: instance

Name  Version         Rev    Tracking     Publisher   Notes
lxd   5.21.3-c5ae129  33110  5.21/stable  canonical✓  -
```

The first part of the version string corresponds to the LXD release (in this sample, `5.21.3`).

(howto-snap-updates-upgrades)=
## Updates versus upgrades

{ref}`ref-snap-updates` of the LXD snap occur within the same channel, whereas {ref}`upgrades <ref-snap-upgrades>` refer to {ref}`changing the tracked snap channel <howto-snap-change>` to use a newer track.

For details, see our {ref}`ref-snap-updates-upgrades` reference guide, including its section on {ref}`ref-snap-downgrades`.

(howto-snap-updates)=
## Manage updates

When LXD is {ref}`installed as a snap <installing-snap-package>`, it begins tracking the specified snap channel, or the most recent stable LTS track if not specified. Whenever a new version is published to that channel, the LXD version on your system automatically updates.

For control over the update schedule, use either of the following approaches:

- {ref}`howto-snap-updates-schedule`.
- {ref}`howto-snap-updates-hold` and perform {ref}`howto-snap-updates-manual` as needed.

For clustered LXD installations, also follow the instructions below to {ref}`synchronize updates for cluster members <howto-snap-updates-sync>`.

For more information about snap updates in general, see the Snap documentation: [Managing updates](https://snapcraft.io/docs/managing-updates).

(howto-snap-updates-schedule)=
### Schedule updates with the refresh timer

Set the [snaps refresh timer](https://snapcraft.io/docs/managing-updates#p-32248-refreshtimer) to regularly update snaps at specific times. This enables you to schedule automatic updates during times that don't disturb normal operation. The refresh timer is set system-wide; you cannot set it for the LXD snap only. It does not apply to snaps that are held indefinitely.

For example, to configure your system to update snaps only between 8:00 am and 9:00 am on Mondays, set the following option:

```bash
  sudo snap set system refresh.timer=mon,8:00-9:00
```

You can also use the [refresh.hold](https://snapcraft.io/docs/managing-updates#p-32248-refreshhold) setting to hold all snap updates for up to 90 days, after which they automatically update. See [Control updates with system options](https://snapcraft.io/docs/managing-updates#heading--refresh-hold) in the snap documentation for details.

(howto-snap-updates-hold)=
### Hold updates

You can hold snap updates for the LXD snap, either indefinitely or for a specific duration. If you want to fully control updates to your LXD snap, you should set up an indefinite hold.

To indefinitely hold updates, run:

```bash
sudo snap refresh --hold lxd
```

Then you can perform {ref}`manual updates <howto-snap-updates-manual>` on a schedule that you control.

For detailed information about holds, including how to hold snaps for a specific duration rather than indefinitely, see: [Pause or stop automatic updates](https://snapcraft.io/docs/managing-updates#p-32248-pause-or-stop-automatic-updates) in the Snap documentation.

(howto-snap-updates-manual)=
### Manual updates

For an LXD snap installed as part of a cluster, see the section on {ref}`synchronizing cluster updates <howto-snap-updates-sync>` below.

Otherwise, run:

```bash
sudo snap refresh lxd
```

This updates your LXD snap to the latest release within its channel.

(howto-snap-updates-sync)=
### Synchronize updates for a LXD cluster cohort

All {ref}`LXD cluster members <exp-clusters>` must run the same LXD version, and ideally the same snap revision of the version. To synchronize updates, set the `--cohort="+"` flag on all cluster members.

You only need to set this flag once per LXD snap. This can occur during {ref}`installation <installing-snap-package>`, or the first time you {ref}`perform a manual update <howto-snap-updates-manual>`.

To set this flag during installation:

```bash
sudo snap install lxd --cohort="+"
```

To set this flag later, during a manual update:

```bash
sudo snap refresh lxd --cohort="+"
```

After you set this flag, `snap list lxd` shows `in-cohort` in the `Notes` column. Example:

```{terminal}
:input: snap list lxd
:user: root
:host: instance

Name  Version         Rev    Tracking     Publisher   Notes
lxd   5.21.3-c5ae129  33110  5.21/stable  canonical✓  in-cohort
```

Subsequent updates to this snap automatically use the `--cohort="+"` flag, even if you {ref}`change its channel <howto-snap-change>` or use automated or {ref}`scheduled <howto-snap-updates-schedule>` updates. Thus, once the snap is `in-cohort`, you can omit that flag for future updates.

````{admonition} Workaround if the cohort flag malfunctions
:class: tip

If for some reason, the `--cohort="+"` flag does not work as expected, you can update using a matching revision on all cluster members manually:

```bash
sudo snap refresh lxd --revision=<revision_number>
```

Example:

```bash
sudo snap refresh lxd --revision=33110
```

````

### Manage updates with an Enterprise Store proxy

```{admonition} For Snap Store Proxy users
:class: tip

If you previously used the Snap Store Proxy, see the [migration guide](https://documentation.ubuntu.com/enterprise-store/main/how-to/migrate) in the Enterprise Store documentation for instructions on transitioning to the Enterprise Store.

```

If you manage a large LXD cluster and require absolute control over when updates are applied, consider using the [Enterprise Store](https://documentation.ubuntu.com/enterprise-store/main/). This proxy application sits between your machines' snap clients and the Snap Store, giving you control over which snap revisions are available for installation.

To get started, follow the Enterprise Store documentation to [install](https://documentation.ubuntu.com/enterprise-store/main/how-to/install/) and [register](https://documentation.ubuntu.com/enterprise-store/main/how-to/register/) the service. Once it's running, configure all cluster members to use the proxy; see [Configure devices](https://documentation.ubuntu.com/enterprise-store/main/how-to/devices/) for instructions. You can then [override the revision](https://documentation.ubuntu.com/enterprise-store/main/how-to/overrides/) for the LXD snap to control which version is installed:

```bash
sudo enterprise-store override lxd <channel>=<revision>
```

Example:

```bash
sudo enterprise-store override lxd stable=25846
```

(howto-snap-change)=
## Change the snap channel

You can change the tracked channel's {ref}`track <ref-snap-tracks>`, its {ref}`risk level <ref-snap-risk>`, or both. A change to a higher track is considered an {ref}`upgrade <ref-snap-upgrades>`.

Downgrading is not supported from higher to lower tracks, and neither is changing from a higher to a lower risk level in the {ref}`latest <ref-snap-tracks-latest>` or {ref}`current feature <ref-snap-track-feature>` track. For details, see: {ref}`ref-snap-downgrades`.

To change the {ref}`channel <ref-snap-channels>` and immediately use the most recent release in the target channel, run:

```bash
sudo snap refresh lxd --channel=<target channel> [--cohort="+"]
```

Include the optional `--cohort="+"` flag only for cluster members who have not previously set this flag before. See: {ref}`howto-snap-updates-sync`.

If you upgrade LXD on cluster members, all members must be upgraded to the same version. For details, see: {ref}`howto-cluster-manage-update-upgrade`.

### Examples

If your current channel is `6/stable`, the following command changes the {ref}`risk level <ref-snap-risk>` only:

```bash
sudo snap refresh lxd --channel=6/edge
```

If your current channel is `5.21/edge`, the following command upgrades LXD to the `6/stable` channel:

```bash
sudo snap refresh lxd --channel=6/stable
```

(howto-snap-configure)=
## Configure the snap

The LXD snap has several configuration options that control the behavior of the installed LXD server.
For example, you can define a LXD user group to achieve a multi-user environment for LXD. For more information, see: {ref}`projects-confine-users`.

See the [LXD snap page](https://snapcraft.io/lxd) for a list of available configuration options.

To set any of these options, run:

```bash
sudo snap set lxd <key>=<value>
```

Example:

```bash
sudo snap set lxd daemon.user.group=lxd-users
```

To see all configuration options that are explicitly set on the snap, run:

```bash
sudo snap get lxd
```

For more information about snap configuration options, visit [Configure snaps](https://snapcraft.io/docs/configuration-in-snaps) in the Snap documentation.

(howto-snap-daemon)=
## Manage the LXD daemon

Installing LXD as a snap creates the LXD daemon as a [snap service](https://snapcraft.io/docs/service-management). Use the following `snap` commands to manage this daemon.

To view the status of the daemon, run:

```bash
snap services lxd
```

To stop the daemon, run:

```bash
sudo snap stop lxd
```

Stopping the daemon also stops all running LXD instances.

To start the LXD daemon, run:

```bash
sudo snap start lxd
```

Starting the daemon also starts all previously running LXD instances.

To restart the daemon, run:

```bash
sudo snap restart lxd
```

This also stops and starts all running LXD instances. To keep the instances running as you restart the daemon, use the `--reload` flag:

```bash
sudo snap restart --reload lxd
```

For more information about managing snap services, visit [Service management](https://snapcraft.io/docs/service-management) in the Snap documentation.

## Related topics

How-to guide:

- {ref}`installing-snap-package`

Reference:

- {ref}`ref-releases-snap`
