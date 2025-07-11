(instances-ubuntu-pro-attach)=
# How to configure Ubuntu Pro guest attachment

If [Ubuntu Pro](https://ubuntu.com/pro) is enabled on a LXD host, guest instances can be automatically attached to the Pro subscription on that host.

(instances-ubuntu-pro-attach-requirements)=
## Requirements

- Ubuntu Pro must be enabled on the LXD host server.
- The Pro client must be updated to the latest version.

(instances-ubuntu-pro-attach-configure)=
## Configure guest attachment

On the LXD host, run:

```bash
sudo pro config set lxd_guest_attach=<on|off|available>
```

The allowed values are:

- `on`: New LXD guest instances are automatically attached to the host's Pro subscription.
- `off`: Default if unset. LXD guest instances cannot be attached to the host's Pro subscription.
- `available`: New LXD guest instances on the host are not attached automatically, but can be attached using the `pro auto-attach` command in the guest.

(instances-ubuntu-pro-attach-auto)=
## Automatic attachment

If `lxd_guest_attach=on` is set, instances automatically attach to its host's Pro subscription at startup. The initial attach process can take some time. To confirm the subscription, run:

```bash
lxc exec <guest-instance> -- pro status
```

If the attach process has not completed, you will see the following lines within the output:

```bash
NOTICES
Operation in progress: pro.daemon.attempt_auto_attach
```

(instances-ubuntu-pro-attach-force)=
## Force attachment

Guest instances that were started prior to setting `lxd_guest_attach=on` on the host will not automatically attach to the host's Pro subscription. Neither will any instances on a host set to `lxd_guest_attach=available`.

To force such instances to attach, run:

```bash
lxc exec <guest-instance> -- pro auto-attach
```

(instances-ubuntu-pro-attach-override)=
## Instance-level override

The `lxd_guest_attach` setting on the host can be overridden at the instance level, through the {config:option}`instance-miscellaneous:ubuntu_pro.guest_attach` configuration option. The `ubuntu_pro.guest_attach` configuration key has three options: `on`, `off`, and `available`.

For example, if `lxd_guest_attach` is set to `on` on the host and you want to prevent Pro attachment in a new guest instance you are launching, run:

```bash
lxc launch ubuntu:24.04 <guest-instance> -c ubuntu_pro.guest_attach=off
```

To set this key on an instance that has already been created, see: {ref}`instances-configure-options`.

All options for Pro guest attachment are described below.

|                     |         `on (host)`         |     `available (host)`      |        `off (host)`       |      `unset (host)`       |
| ------------------- | --------------------------- | --------------------------- | ------------------------- | ------------------------- |
|        `on (guest)` | auto-attach on start        |  auto-attach on start       | guest attachment disabled | guest attachment disabled |
| `available (guest)` | attach on `pro auto-attach` | attach on `pro-auto-attach` | guest attachment disabled | guest attachment disabled |
|       `off (guest)` | guest attachment disabled   | guest attachment disabled   | guest attachment disabled | guest attachment disabled |
|     `unset (guest)` | auto-attach on start        | attach on `pro-auto-attach` | guest attachment disabled | guest attachment disabled |
