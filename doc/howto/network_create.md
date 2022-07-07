# How to create and configure a network

To create and configure a managed network, use the `lxc network` command and its subcommands.
Append `--help` to any command to see more information about its usage and available flags.

(network-types)=
## Network types

The following network types are available:

```{list-table}
   :header-rows: 1

* - Network type
  - Documentation
  - Configuration options
* - `bridge`
  - {ref}`network-bridge`
  - {ref}`network-bridge-options`
* - `ovn`
  - {ref}`network-ovn`
  - {ref}`network-ovn-options`
* - `macvlan`
  - {ref}`network-macvlan`
  - {ref}`network-macvlan-options`
* - `sriov`
  - {ref}`network-sriov`
  - {ref}`network-sriov-options`
* - `physical`
  - {ref}`network-physical`
  - {ref}`network-physical-options`

```

## Create a network

Use the following command to create a network:

```bash
lxc network create <name> --type=<network_type> [configuration_options...]
```

See {ref}`network-types` for a list of available network types and links to their configuration options.

If you do not specify a `--type` argument, the default type of `bridge` is used.

### Create a network in a cluster

If you are running a LXD cluster and want to create a network, you must create the network for each cluster member separately.
The reason for this is that the network configuration, for example, the name of the parent network interface, might be different between cluster members.

Therefore, you must first create a pending network on each member with the `--target=<cluster_member>` flag and the appropriate configuration for the member.
Make sure to use the same network name for all members.
Then create the network without specifying the `--target` flag to actually set it up.

For example, the following series of commands sets up a physical network with the name `UPLINK` on three cluster members:

```bash
lxc network create UPLINK --type=physical parent=br0 --target=vm01
lxc network create UPLINK --type=physical parent=br0 --target=vm02
lxc network create UPLINK --type=physical parent=br0 --target=vm03
lxc network create UPLINK --type=physical
```

## Configure a network

To configure an existing network, use either the `lxc network set` and `lxc network unset` commands (to configure single settings) or the `lxc network edit` command (to edit the full configuration).
To configure settings for specific cluster members, add the `--target` flag.

For example, the following command configures a DNS server for a physical network:

```bash
lxc network set UPLINK dns.nameservers=8.8.8.8
```

The available configuration options differ depending on the network type.
See {ref}`network-types` for links to the configuration options for each network type.

There are separate commands to configure advanced networking features.
See the following documentation:

- {doc}`/howto/network_acls`
- {doc}`/howto/network_forwards`
- {doc}`/howto/network_load_balancers`
- {doc}`/howto/network_zones`
- {doc}`/howto/network_ovn_peers` (OVN only)
