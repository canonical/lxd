(network-create)=
# How to create a network

````{only} integrated

```{admonition} For MicroCloud users
:class: note
The MicroCloud setup process creates a network. Thus, you do not need to follow the steps on this page. After MicroCloud setup, LXD networking commands can be used with the cluster.
```

````

To create a managed network, use the [`lxc network`](lxc_network.md) command and its subcommands.
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

`````{tabs}
````{group-tab} CLI

Use the following command to create a network:

```bash
lxc network create <name> --type=<network_type> [configuration_options...]
```

See {ref}`network-types` for a list of available network types and links to their configuration options.

If you do not specify a `--type` argument, the default type of `bridge` is used.

(network-create-cluster)=
### Create a network in a cluster

If you are running a LXD cluster and want to create a network, you must create the network for each cluster member separately.
The reason for this is that the network configuration, for example, the name of the parent network interface, might be different between cluster members.

Therefore, you must first create a pending network on each member with the `--target=<cluster_member>` flag and the appropriate configuration for the member.
Make sure to use the same network name for all members.
Then create the network without specifying the `--target` flag to actually set it up.

For example, the following series of commands sets up a physical network with the name `UPLINK` on three cluster members:

```{terminal}
lxc network create UPLINK --type=physical parent=br0 --target=vm01

Network UPLINK pending on member vm01
```

```{terminal}
lxc network create UPLINK --type=physical parent=br0 --target=vm02

Network UPLINK pending on member vm02
```

```{terminal}
lxc network create UPLINK --type=physical parent=br0 --target=vm03

Network UPLINK pending on member vm03
```

```{terminal}
lxc network create UPLINK --type=physical

Network UPLINK created
```

Also see {ref}`cluster-config-networks`.
````

```` {group-tab} UI

From the main navigation, select {guilabel}`Networks`.

On the resulting page, click {guilabel}`Create network` in the upper-right corner.

You can then configure the network name and type, as well as other attributes. Optional additional attributes are split into the categories {guilabel}`Bridge`, {guilabel}`IPv4`, {guilabel}`IPv6` and {guilabel}`DNS`, which can be seen in the submenu on the right.

Click {guilabel}`Create` to create the network.

```{figure} /images/networks/network_create.png
:width: 80%
:alt: Create a network in LXD
```

````
`````

(network-attach)=
## Attach a network to an instance

`````{tabs}
````{group-tab} CLI

After creating a managed network, you can attach it to an instance as a {ref}`NIC device <devices-nic>`.

To do so, use the following command:

    lxc network attach <network_name> <instance_name> [<device_name>] [<interface_name>]

The device name and the interface name are optional, but we recommend specifying at least the device name.
If not specified, LXD uses the network name as the device name, which might be confusing and cause problems.
For example, LXD images perform IP auto-configuration on the `eth0` interface, which does not work if the interface is called differently.

For example, to attach the network `my-network` to the instance `my-instance` as `eth0` device, enter the following command:

    lxc network attach my-network my-instance eth0


````
```` {group-tab} UI

When {ref}`creating <instances-create>` or {ref}`configuring an instance <instances-configure>`, go to the {guilabel}`Devices` section in the left-hand submenu, then select {guilabel}`Network` to view and edit the networks linked to the instance.

```{figure} /images/networks/network_add_to_instance.png
:width: 80%
:alt: Add a network to an instance in LXD
```

Click the {guilabel}`Attach network` button to add a new network. From here, you can select an existing network from the {guilabel}`Network` dropdown and assign it a device name.

```{figure} /images/networks/network_attach_instance.png
:width: 80%
:alt: Attach a network to an instance in LXD
```

If configuring an instance, select {guilabel}`Save changes` to save your changes. If creating an instance, select {guilabel}`Create` to create your instance.

````
`````

### Attach the network as a device

The [`lxc network attach`](lxc_network_attach.md) command is a shortcut for adding a NIC device to an instance.
Alternatively, you can add a NIC device based on the network configuration in the usual way:

    lxc config device add <instance_name> <device_name> nic network=<network_name>

When using this way, you can add further configuration to the command to override the default settings for the network if needed.
See {ref}`NIC device <devices-nic>` for all available device options.
