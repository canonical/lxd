(instances-ubuntu-pro-attach)=
# How to configure Ubuntu Pro guest attachment

If Ubuntu Pro is enabled on a LXD host, guest instances can be automatically attached to the Pro subscription.

First, the Pro client on the host machine must be configured to allow guest attachment:

```bash
sudo pro config set lxd_guest_attach=on
```

The allowed values are `on`, `off`, and `available`.
If unset, this defaults to `off`.

```{note}
The Pro client must be updated to the latest version.
```

You can now launch an Ubuntu instance:

```bash
lxc launch ubuntu:24.04 pro-guest
```

The instance will automatically attach to the Pro subscription at start up.

Pro attachment can take some time.
You can check the status using:

```bash
lxc exec pro-guest -- pro status
```

The `lxd_guest_attach` setting can be overridden by the {config:option}`instance-miscellaneous:ubuntu_pro.guest_attach` configuration option.
For example, if `lxd_guest_attach` is set to `on` on the host, to prevent Pro attachment in the guest you can run:

```bash
lxc launch ubuntu:24.04 non-pro-guest -c ubuntu_pro.guest_attach=off
```

The `ubuntu_pro.guest_attach` configuration key has three options: `on`, `off`, and `available`.

All options for Pro guest attachment are described below.

|                     |         `on (host)`         |     `available (host)`      |        `off (host)`       |      `unset (host)`       |
| ------------------- | --------------------------- | --------------------------- | ------------------------- | ------------------------- |
|        `on (guest)` | auto-attach on start        |  auto-attach on start       | guest attachment disabled | guest attachment disabled |
| `available (guest)` | attach on `pro auto-attach` | attach on `pro-auto-attach` | guest attachment disabled | guest attachment disabled |
|       `off (guest)` | guest attachment disabled   | guest attachment disabled   | guest attachment disabled | guest attachment disabled |
|     `unset (guest)` | auto-attach on start        | attach on `pro-auto-attach` | guest attachment disabled | guest attachment disabled |
