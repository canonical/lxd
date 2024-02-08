---
discourse: ubuntu:37214
---

(howto-snap)=
# How to manage the LXD snap

Among {ref}`other options <installing-other>`, LXD is distributed as a [snap](https://snapcraft.io/docs).
The benefit of packaging LXD as a snap is that it makes it possible to include all of LXD’s dependencies in one package, and that it allows LXD to be installed on many different Linux distributions.
The snap ensures that LXD runs in a consistent environment.

## Control updates of the snap

When running LXD in a production environment, you must make sure to have a suitable version of the snap installed on all machines of your LXD cluster.

### Choose the right channel and track

Snaps come with different channels that define which release of a snap is installed and tracked for updates.
See [Channels and tracks](https://snapcraft.io/docs/channels) in the snap documentation for detailed information.

Feature releases of LXD are available on the `latest` track.
In addition, LXD provides tracks for the supported feature releases.
See {ref}`installing-release` for more information.

On all tracks, the `stable` risk level contains all fixes and features for the respective track, but it is only updated when the LXD team decides that a feature is ready and no issues have been revealed by users running the same revision on higher risk levels (`edge` and `candidate`).

When installing a snap, specify the channel as follows:

    sudo snap install <snap_name> --channel=<channel>

For example:

    sudo snap install lxd --channel=latest/stable

If you do not specify a channel, snap will choose the default channel (the latest LTS release).

To see all available channels of the LXD snap, run the following command:

    snap info lxd

### Hold and schedule updates

By default, snaps are updated automatically.
In the case of LXD, this can be problematic because all machines of a cluster must use the same version of the LXD snap.

Therefore, you should schedule your updates and make sure that all cluster members are in sync regarding the snap version that they use.

#### Schedule updates

There are two methods for scheduling when your snaps should be updated:

- You can hold snap updates for a specific time, either for specific snaps or for all snaps on your system.
  After the duration of the hold, or when you remove the hold, your snaps are automatically refreshed.
- You can specify a system-wide refresh window, so that snaps are automatically refreshed only within this time frame.
  Such a refresh window applies to all snaps.

Hold updates
: You can hold snap updates for a specific time or forever, for all snaps or only for the LXD snap.
  If you want to fully control updates to your LXD deployment, you should put a hold on the LXD snap until you decide to update it.

  Enter the following command to indefinitely hold all updates for the LXD snap:

      sudo snap refresh --hold lxd

  When you choose to update your installation, use the following commands to remove the hold, update the snap, and hold the updates again:

      sudo snap refresh --unhold lxd
      sudo snap refresh lxd --cohort="+"
      sudo snap refresh --hold lxd

  See [Hold refreshes](https://snapcraft.io/docs/managing-updates#heading--hold) in the snap documentation for detailed information about holding snap updates.

Specify a refresh window
: Depending on your setup, you might want your snaps to update regularly, but only at specific times that don't disturb normal operation.

  You can achieve this by specifying a refresh timer.
  This option defines a refresh window for all snaps that are installed on the system.

  For example, to configure your system to update snaps only between 8:00 am and 9:00 am on Mondays, set the following option:

      sudo snap set system refresh.timer=mon,8:00-9:00

  You can use a similar mechanism (setting `refresh.hold`) to hold snap updates as well.
  However, in this case the snaps will be refreshed after 90 days, irrespective of the value of `refresh.hold`.

  See [Control updates with system options](https://snapcraft.io/docs/managing-updates#heading--refresh-hold) in the snap documentation for detailed information.

#### Keep cluster members in sync

The cluster members that are part of the LXD deployment must always run the same version of the LXD snap.
This means that when the snap on one of the cluster members is refreshed, it must also be refreshed on all other cluster members before the LXD cluster is operational again.

Snap updates are delivered as [progressive releases](https://snapcraft.io/docs/progressive-releases), which means that updated snap versions are made available to different machines at different times.
This method can cause a problem for cluster updates if some cluster members are refreshed to a version that is not available to other cluster members yet.

To avoid this problem, use the `--cohort="+"` flag when refreshing your snaps:

    sudo snap refresh lxd --cohort="+"

This flag ensures that all machines in a cluster see the same snap revision and are therefore not affected by a progressive rollout.

### Use a Snap Store Proxy

If you manage a large LXD cluster and you need absolute control over when updates are applied, consider installing a Snap Store Proxy.

The Snap Store Proxy is a separate application that sits between the snap client command on your machines and the snap store.
You can configure the Snap Store Proxy to make only specific snap revisions available for installation.

See the [Snap Store Proxy documentation](https://docs.ubuntu.com/snap-store-proxy/) for information about how to install and register the Snap Store Proxy.

After setting it up, configure the snap clients on all cluster members to use the proxy.
See [Configuring snap devices](https://docs.ubuntu.com/snap-store-proxy/en/devices) for instructions.

You can then configure the Snap Store Proxy to override the revision for the LXD snap:

    sudo snap-proxy override lxd <channel>=<revision>

For example:

    sudo snap-proxy override lxd stable=25846

## Configure the snap

The LXD snap has several configuration options that control the behavior of the installed LXD server.
For example, you can define a LXD user group to achieve a multi-user environment for LXD (see {ref}`projects-confine-users` for more information).

See the [LXD snap page](https://snapcraft.io/lxd) for a list of available configuration options.

To set any of these options, use the following command:

    sudo snap set lxd <key>=<value>

For example:

    sudo snap set lxd daemon.user.group=lxd-users

To see all configuration options that are set on the snap, use the following command:

    sudo snap get lxd

```{note}
This command returns only configuration options that have been explicitly set.
```

See [Configure snaps](https://snapcraft.io/docs/configuration-in-snaps) in the snap documentation for more information about snap configuration options.

## Start and stop the daemon

To start and stop the LXD daemon, you can use the `start` and `stop` commands of the snap:

    sudo snap stop lxd
    sudo snap start lxd

These commands are equivalent to running the corresponding `systemctl` commands:

    sudo systemctl stop snap.lxd.daemon.service snap.lxd.daemon.unix.socket
    sudo systemctl start snap.lxd.daemon.unix.socket; lxc list

Stopping the daemon also stops all running instances.

To restart the LXD daemon, use the following command:

    sudo systemctl restart snap.lxd.daemon

Restarting the daemon stops all running instances.
If you want to keep the instances running, reload the daemon instead:

    sudo systemctl reload snap.lxd.daemon

```{note}
To restart the daemon, you can also use the snap commands.
To stop all running instances and restart:

    sudo snap restart lxd

To keep the instances running and reload:

    sudo snap restart --reload lxd

However, there is currently a [bug in `snapd`](https://bugs.launchpad.net/snapd/+bug/2028141) that causes undesired side effects when using the `snap restart` command.
Therefore, we recommend using the `systemctl` commands instead.
```
