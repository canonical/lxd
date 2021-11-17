# cloud-init

LXD supports [cloud-init](https://launchpad.net/cloud-init) via the following instance or profile
configuration keys

* `cloud-init.vendor-data`
* `cloud-init.user-data`
* `cloud-init.network-config`

Before trying to use it, however, first determine which image source you are
about to use as not all images have the `cloud-init` package installed.

The images from the `ubuntu` and `ubuntu-daily` remotes are all `cloud-init` enabled.
Images from the `images` remote have `cloud-init` enabled variants using the `/cloud` suffix, e.g. `images:ubuntu/20.04/cloud`.

Both `vendor-data` and `user-data` follow the same rules, with the following caveats:
* Users have ultimate control over vendordata. They can disable its execution or disable handling of specific parts of multipart input.
* By default it only runs on first boot
* Vendordata can be disabled by the user. If the use of vendordata is required for the instance to run, then vendordata should not be used.
* user supplied cloud-config is merged over cloud-config from vendordata.

For LXD instances, `vendor-data` should be used in profiles rather than the instance config.

Cloud-config examples can be found here: https://cloudinit.readthedocs.io/en/latest/topics/examples.html

## Custom network configuration

cloud-init uses the network-config data to render the relevant network
configuration on the system using either ifupdown or netplan depending
on the Ubuntu release.

The default behavior is to use a DHCP client on an instance's eth0 interface.

In order to change this you need to define your own network configuration
using `cloud-init.network-config` key in the config dictionary which will override
the default configuration (this is due to how the template is structured).

For example, to configure a specific network interface with a static IPv4
address and also use a custom nameserver use

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
 * `/etc/network/interfaces.d/50-cloud-init.cfg` (if using ifupdown)
 * `/etc/netplan/50-cloud-init.yaml` (if using netplan)
