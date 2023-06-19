---
discourse: 12559
relatedlinks: https://cloudinit.readthedocs.org/
---

(cloud-init)=
# cloud-init

```{youtube} https://www.youtube.com/watch?v=8OCG15TAldI
```
`cloud-init` is a software for automatic customization of a Linux distribution.

Features include:

* install packages
* apply/edit configuration
* add users
* and more

See the [Cloud-init documentation](https://cloudinit.readthedocs.io/en/latest/).

## `cloud-init` support in images

Before trying to use `cloud-init`, however, first determine which image source you are
about to use as not all images have the `cloud-init` package installed.

The images from the `ubuntu` and `ubuntu-daily` remotes are all `cloud-init` enabled.
Images from the `images` remote have `cloud-init` enabled variants using the `/cloud` suffix, e.g. `images:ubuntu/22.04/cloud`.

Requirements:

* Images with cloud-init support: For example, official LXD images that contain the term `cloud` in `ALIAS` have implemented cloud-init support.

## Configuration keys

LXD supports [cloud-init](https://launchpad.net/cloud-init) via the following instance or profile
configuration keys:

* `cloud-init.vendor-data`
* `cloud-init.user-data`
* `cloud-init.network-config`

- `user.meta-data` - see [cloud-init docs - instance metadata](https://cloudinit.readthedocs.io/en/latest/explanation/instancedata.html)
- `user.vendor-data` - see [cloud-init docs - vendordata](https://cloudinit.readthedocs.io/en/latest/explanation/vendordata.html)
- `user.network-config` - see [cloud-init docs - network configuration](https://cloudinit.readthedocs.io/en/latest/reference/network-config.html)

For more information, see the [`cloud-init` instance options](instance-options-cloud-init), and the documentation for the [LXD data source](https://cloudinit.readthedocs.io/en/latest/reference/datasources/lxd.html) in the `cloud-init` documentation.

Both `vendor-data` and `user-data` follow the same rules, with the following caveats:

* Users have ultimate control over vendor data. They can disable its execution or disable handling of specific parts of multipart input.
* By default it only runs on first boot.
* Vendor data can be disabled by the user. If the use of vendor data is required for the instance to run, then vendor data should not be used.
* User supplied `cloud-config` is merged over `cloud-config` from vendor data.

For LXD instances, `vendor-data` should be used in profiles rather than the instance configuration.

```{note}
If both `cloud-init.user-data` and `cloud-init.vendor-data` are supplied, `cloud-init` merges the two configurations.

However, if you use the same keys in both configurations, merging might not be possible.
In this case, configure how `cloud-init` should merge the provided data.
See [Merging User-Data Sections](https://cloudinit.readthedocs.io/en/latest/reference/merging.html) in the `cloud-init` documentation for instructions.
```

## How to work with `cloud-init`

For a safe way to test, use a new profile that's copied from the default profile.

    lxc profile copy default test

Then edit the new `test` profile. You might want to set your `EDITOR` environment variable first.

    lxc profile edit test

For a new LXD installation, the configuration file should look similar to this example:

```yaml
config: {}
description: Default LXD profile
devices:
  eth0:
    name: eth0
    network: lxdbr0
    type: nic
  root:
    path: /
    pool: default
    type: disk
```

Once you've set up the `cloud-init` configuration, use `lxc launch` with `--profile <profilename>` to apply the profile to the instance.

### Adding cloud-init keys to the configuration

`cloud-init` keys require a specific syntax. You use a pipe symbol (`|`) to indicate that all indented text after the pipe should be passed to `cloud-init` as a single string, with new lines and indentation preserved; this is [literal style format](https://yaml.org/spec/1.2.2/#812-literal-style) used in YAML.

```yaml
config:
  cloud-init.user-data: |
```

```yaml
config:
  cloud-init.vendor-data: |
```

```yaml
config:
  cloud-init.network-config: |
```

!!! Tip
    You can check whether the syntax is correct with: [cloud-init FAQ - debug user-data](https://cloudinit.readthedocs.io/en/latest/reference/faq.html#how-can-i-debug-my-user-data)

!!! note
	Cloud-init may take a while until it is finished, depending on your instructions.

#### Cloud-init status
You can get the status of cloud-init with:

	cloud-init status

Reports:

`status: running`: means cloud-init is still working

or

`status: done`: means cloud-init has finished work

<br>

You can also use the following flag, which will only respond when cloud-init is finished:

	cloud-init status --wait


## How to specify user or vendor data

cloud-init uses the `user-data` (and `vendor-data`) section to do things like upgrade packages, install packages or run arbitrary commands.

A `cloud-init.user-data` key must have a first line that indicates what type of [data format](https://cloudinit.readthedocs.io/en/latest/topics/format.html) is being passed to `cloud-init`. For activities like upgrading packages or setting up a user, `#cloud-config` is the data format to use.

An instance's rootfs will contain the following files as a result:

* `/var/lib/cloud/instance/cloud-config.txt`
* `/var/lib/cloud/instance/user-data.txt`

### Examples for `cloud-init` user data

`cloud-config` examples can be found in the [`cloud-init` documentation](https://cloudinit.readthedocs.io/en/latest/topics/examples.html).


#### Upgrade packages on instance creation

To trigger a package upgrade from the repositories for the instance, use the `package_upgrade` key:

```yaml
config:
  cloud-init.user-data: |
    #cloud-config
    package_upgrade: true
```

#### Install packages on instance creation

To install specific packages when the instance is set up, use the `packages` key and specify the package names as a list:

```yaml
config:
  cloud-init.user-data: |
    #cloud-config
    packages:
      - git
      - openssh-server
```

#### Set the time zone on instance creation

To set the time zone for the instance, use the `timezone` key:

```yaml
config:
  cloud-init.user-data: |
    #cloud-config
    timezone: Europe/Rome
```

#### Run commands

To run a command (such as writing a marker file), use the `runcmd` key and specify commands as a list:

```yaml
config:
  cloud-init.user-data: |
    #cloud-config
    runcmd:
      - [touch, /run/cloud.init.ran]
```

#### Add a user account

To add a user account, use the `user` key. See the [documentation](https://cloudinit.readthedocs.io/en/latest/topics/examples.html#including-users-and-groups) for more details about default users and which keys are supported.

```yaml
config:
  cloud-init.user-data: |
    #cloud-config
    user:
      - name: documentation_example
```

## How to specify network configuration data

`cloud-init` uses the `network-config` data to render the relevant network
configuration on the system using either `ifupdown` or `netplan` depending
on the Ubuntu release.

The default behavior is to use a DHCP client on an instance's `eth0` interface.

In order to change this you need to define your own network configuration
using `cloud-init.network-config` key in the configuration dictionary which will override
the default configuration (this is due to how the template is structured).

### Examples for a `cloud-init` network configuration

For example, to configure a specific network interface with a static IPv4
address and also use a custom name server use

```yaml
config:
  cloud-init.network-config: |
    version: 1
    config:
      - type: physical
        name: eth1
        subnets:
          - type: static
            ipv4: true
            address: 10.10.101.20
            netmask: 255.255.255.0
            gateway: 10.10.101.1
            control: auto
      - type: nameserver
        address: 10.10.10.254
```

An instance's rootfs will contain the following files as a result:

* `/var/lib/cloud/seed/nocloud-net/network-config`
* `/etc/network/interfaces.d/50-cloud-init.cfg` (if using `ifupdown`)
* `/etc/netplan/50-cloud-init.yaml` (if using `netplan`)
