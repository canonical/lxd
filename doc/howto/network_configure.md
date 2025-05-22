(network-configure)=
# How to configure a network

`````{tabs}
````{group-tab} CLI
To configure an existing network, use either the [`lxc network set`](lxc_network_set.md) and [`lxc network unset`](lxc_network_unset.md) commands (to configure single settings) or the `lxc network edit` command (to edit the full configuration).
To configure settings for specific cluster members, add the `--target` flag.

For example, the following command configures a DNS server for a physical network:

```bash
lxc network set UPLINK dns.nameservers=8.8.8.8
```

The available configuration options differ depending on the network type.
See {ref}`network-types` for links to the configuration options for each network type.
````
````{group-tab} UI
To edit the configuration of a network, navigate to the overview page for the network, and observe its attributes and settings.

Within the Configuration tab, you can edit key settings of the network by clicking on the {guilabel}`Edit` pencil icon inline with the desired configuration setting.

```{figure} /images/networks/network_configuration.png
:width: 80%
:alt: LXD Network overview page
```

````
`````

There are separate commands to configure advanced networking features.
See the following documentation:

- {doc}`/howto/network_acls`
- {doc}`/howto/network_forwards`
- {doc}`/howto/network_load_balancers`
- {doc}`/howto/network_zones`
- {doc}`/howto/network_ovn_peers` (OVN only)
