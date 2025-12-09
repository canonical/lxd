---
discourse: lxc:[OVN&#32;network&#32;to&#32;network&#32;routing](12165)
---

(network-ovn-peers)=
# How to create OVN peer routing relationships

````{only} diataxis
```{important}
This guide applies to OVN networks only.
```
````

By default, traffic between two OVN networks goes through the uplink network.
This path is inefficient, however, because packets must leave the OVN subsystem and transit through the host's networking stack (and, potentially, an external network) and back into the OVN subsystem of the target network.
Depending on how the host's networking is configured, this might limit the available bandwidth (if the OVN overlay network is on a higher bandwidth network than the host's external network).

Therefore, LXD allows creating peer routing relationships between two OVN networks.
Using this method, traffic between the two networks can go directly from one OVN network to the other and thus stays within the OVN subsystem, rather than transiting through the uplink network.

## Create a routing relationship between networks

To add a peer routing relationship between two networks, you must create a network peering for both networks.
The relationship must be mutual.
If you set it up on only one network, the routing relationship will be in pending state, but not active.

When creating the peer routing relationship, specify a peering name that identifies the relationship for the respective network.
The name can be chosen freely, and you can use it later to edit or delete the relationship.

```{admonition} Security notes
:class: note
If the project or the network name is incorrect, the command does not return any error indicating that the respective project/network does not exist, and the routing relationship remains in pending state.
This behavior prevents users in a different project from discovering whether a project and network exists.
```

`````{tabs}
````{group-tab} CLI

Use the following commands to create a peer routing relationship between networks in the same project:

    lxc network peer create <network1> <peering_name> <network2> [configuration_options]
    lxc network peer create <network2> <peering_name> <network1> [configuration_options]

You can also create peer routing relationships between OVN networks in different projects:

    lxc network peer create <network1> <peering_name> <project2/network2> [configuration_options] --project=<project1>
    lxc network peer create <network2> <peering_name> <project1/network1> [configuration_options] --project=<project2>

````
````{group-tab} UI

From the {guilabel}`Networks` page of the {ref}`web UI <access-ui>`, select the desired OVN network. On the network's {guilabel}`Local Peerings` tab, click {guilabel}`Create local peering`.

Fill in all required fields in the {guilabel}`Create local peering` panel.

```{figure} /images/networks/network_create_local_peerings.png
:width: 60%
:alt: View a list of local peerings on a network
```

Target projects and networks for which you have read permission are available from the dropdown selectors. If you want to use a project or network not available in the dropdown, choose the {guilabel}`Manually enter` option.

To create a mutual peering between two networks, click the {guilabel}`Create mutual peering` checkbox. You must have edit permissions for both networks, and you cannot manually enter the target project or the network.

````
`````

### Peering properties

Peer routing relationships have the following properties:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group network-peering-peering-properties start -->
    :end-before: <!-- config group network-peering-peering-properties end -->
```

## List routing relationships

`````{tabs}
````{group-tab} CLI

To list all network peerings for a network, use the following command:

    lxc network peer list <network>

````
````{group-tab} UI

From the {guilabel}`Networks` page of the {ref}`web UI <access-ui>`, select the desired OVN network. View the network's {guilabel}`Local peerings` tab:

```{figure} /images/networks/network_list_local_peerings.png
:width: 95%
:alt: View a list of local peerings on a network
```
````
`````

## Edit a routing relationship

`````{tabs}
````{group-tab} CLI

Use the following command to edit a network peering:

    lxc network peer edit <network> <peering_name>

This command opens the network peering in YAML format for editing.

````
````{group-tab} UI

From the {guilabel}`Networks` page of the {ref}`web UI <access-ui>`, select the desired OVN network. You can edit peerings from the network's {guilabel}`Local peerings` tab. Only the {guilabel}`Description` field can be edited.

```{figure} /images/networks/network_edit_local_peerings.png
:width: 95%
:alt: Edit a local peering on a network
```
````
`````
