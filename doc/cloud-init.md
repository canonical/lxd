# Custom network configuration with cloud-init

[cloud-init](https://launchpad.net/cloud-init) may be used for custom network configuration of containers.

Before trying to use it, however, first determine which image source you are
about to use as not all container images have cloud-init package installed.
At the time of writing, images provided at images.linuxcontainers.org do not
have the cloud-init package installed, therefore, any of the configuration
options mentioned in this guide will not work. On the contrary, images
provided at cloud-images.ubuntu.com have the necessary package installed
and also have a templates directory in their archive populated with

 * `cloud-init-meta.tpl`
 * `cloud-init-user.tpl`
 * `cloud-init-vendor.tpl`
 * `cloud-init-network.tpl`

and others not related to cloud-init.

Templates provided with container images at cloud-images.ubuntu.com have
the following in their `metadata.yaml`:

```yaml
/var/lib/cloud/seed/nocloud-net/network-config:
  when:
    - create
    - copy
  template: cloud-init-network.tpl
```

Therefore, either when you create or copy a container it gets a newly rendered
network configuration from a pre-defined template.

cloud-init uses the network-config file to render the relevant network
configuration on the system using either ifupdown or netplan depending
on the Ubuntu release.

The default behavior is to use a DHCP client on a container's eth0 interface.

In order to change this you need to define your own network configuration
using user.network-config key in the config dictionary which will override
the default configuration (this is due to how the template is structured).

For example, to configure a specific network interface with a static IPv4
address and also use a custom nameserver use

```yaml
config:
  user.network-config: |
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

A container's rootfs will contain the following files as a result:

 * `/var/lib/cloud/seed/nocloud-net/network-config`
 * `/etc/network/interfaces.d/50-cloud-init.cfg` (if using ifupdown)
 * `/etc/netplan/50-cloud-init.yaml` (if using netplan)

# Implementation Details

cloud-init allows you to seed instance configuration using the following files
located at `/var/lib/cloud/seed/nocloud-net`:

 * `user-data` (required)
 * `meta-data` (required)
 * `vendor-data` (optional)
 * `network-config` (optional)

The network-config file is written to by LXD using data provided in templates
that come with an image. This is governed by metadata.yaml but naming of the
configuration keys and template content is not hard-coded as far as LXD is
concerned - this is purely image data that can be modified if needed.

 * [NoCloud data source documentation](https://cloudinit.readthedocs.io/en/latest/topics/datasources/nocloud.html)
 * The source code for [NoCloud data source](https://git.launchpad.net/cloud-init/tree/cloudinit/sources/DataSourceNoCloud.py)
 * A good reference on which values you can use are [unit tests for cloud-init](https://git.launchpad.net/cloud-init/tree/tests/unittests/test_datasource/test_nocloud.py#n163)
 * [cloud-init directory layout](https://cloudinit.readthedocs.io/en/latest/topics/dir_layout.html)

A default `cloud-init-network.tpl` provided with images from the "ubuntu:" image
source looks like this:

```
{% if config\_get("user.network-config", "") == "" %}version: 1
config:
    - type: physical
      name: eth0
      subnets:
          - type: {% if config_get("user.network_mode", "") == "link-local" %}manual{% else %}dhcp{% endif %}
            control: auto{% else %}{{ config_get("user.network-config", "") }}{% endif %}
```

The template syntax is the one used in the pongo2 template engine. A custom
`config_get` function is defined to retrieve values from a container
configuration.

Options available with such a template structure:

 * Use DHCP by default on your eth0 interface;
 * Set `user.network_mode` to `link-local` and configure networking by hand;
 * Seed cloud-init by defining `user.network-config`.
