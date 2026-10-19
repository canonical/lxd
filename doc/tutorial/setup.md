(tutorial-requirements)=
## Requirements

- At least 20 GiB free disk space
- A Linux distribution installed

(tutorial-install)=
## Install LXD using snap

This section of the tutorial assumes that you have the `snap` packaging system available on your system, which is the recommended way to install and update LXD.

To install LXD using `snap`, run:

```bash
sudo snap install lxd
```

```{admonition} If snap is not installed or not supported
:class: note

If you see an error message indicating that `snap` is not installed, visit the {ref}`Snap installation documentation <snap:tutorials-install-the-daemon-index>` and follow the instructions there to install it.

If you use a Linux distribution that does not support `snap`, see {ref}`installing-other` to install LXD by other means if possible, then skip to the {ref}`next section <tutorial-adduser>` of this tutorial.
```

### If the LXD snap is already installed

This tutorial is designed for LXD version {{current_lts_track}} and higher. If you see an error message that the LXD snap is already installed, run the following command to find the channel the snap is tracking:

```bash
snap list lxd
```

The `Tracking` column lists the installed {ref}`snap channel <ref-snap-channels>`. If the number shown is {{current_lts_track}} or higher, run the following command to update the snap to the most recent release in its channel:

```bash
sudo snap refresh lxd
```

Otherwise, if the number shown is lower, an older version is installed. In this case, upgrade to the {{current_lts_track}}/stable channel by following the instructions in this guide: {ref}`howto-snap-change`.

(tutorial-adduser)=
## Add the current user to the `lxd` group

````{admonition} Important security notice
:class: important
% Include content from ../../README.md
```{include} ../../README.md
    :start-after: <!-- Include start security note -->
    :end-before: <!-- Include end security note -->
```
For more information, see {ref}`security-daemon-access`.
````

Installing LXD through its snap should automatically create a `lxd` group on your system. The user you are logged in as must be in this group to interact with LXD.

Enter the following commands. The first command adds your user to the `lxd` group.
The second command starts a new shell where the group membership takes effect immediately.

```bash
sudo usermod -aG lxd "$USER"
newgrp lxd
```

(tutorial-initialize)=
## Initialize LXD

Next, initialize LXD using a minimal setup with default options.

Run:

```bash
lxd init --minimal
```

If this command results in an error message, your group membership might not have taken effect. In this case, close and re-open your terminal, then try again.

If you do not see any message when running this command, that means it has succeeded. Congratulations! You have successfully installed and initialized LXD. Continue on to learn how to use some of LXD's core features.

```{admonition} About the initialization options
:class: tip

The `lxd init` command can be run again later to update the options. Once you have learned more about LXD, you might want to tune the {ref}`initialization options <initialize>` according to your own preferences, or learn how to {ref}`use a preseed file <initialize-preseed>` to initialize LXD from a saved set of options. For now, the minimal configuration is sufficient.
```

(tutorial-enable-ui)=
## Enable the LXD UI

While the installation and initialization steps must be performed via the command line interface, a graphical interface (the LXD UI) is available for use after these setup steps. The LXD UI is accessed through your web browser.

If you prefer to use the LXD UI, expand and follow the steps below.

````{dropdown} View steps to enable the LXD UI

### Expose the LXD server to the local network

By default, LXD is exposed through a Unix socket only and is not accessible over HTTPS. To access and manage LXD through a web browser using HTTPS, we must set the {config:option}`server-core:core.https_address` server configuration option. We will use the local network by configuring this to the IPv4 loopback address `127.0.0.1` and port 8443. Run:

```bash
lxc config set core.https_address 127.0.0.1:8443
```

Confirm that the `core.https_address` option has been set:

```{terminal}
lxc config get core.https_address

127.0.0.1:8443
```

```{admonition} core.https_address
:class: tip

For simplicity, the tutorial asks you to set the `core.https_address` to the IPv4 loopback address. If you prefer, you can set this later to the IPv6 loopback address (`[::1]:8443`), or to an address that is reachable over a network for remote access.
```

### Set up UI access

Go to this URL in your browser: [`https://localhost:8443`](https://localhost:8443)

```{include} ../howto/access_ui.md
:start-after: <!-- Include start access UI -->
:end-before: <!-- Include end access UI -->
```
````

As you continue on with this tutorial, notice that many of the following sections include sets of tabs. When the `UI` tab is available, you can now use the instructions in that tab.


