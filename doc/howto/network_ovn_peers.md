---
discourse: 12165
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

Use the following commands to create a peer routing relationship between networks in the same project:

    lxc network peer create <network1> <peering_name> <network2> [configuration_options]
    lxc network peer create <network2> <peering_name> <network1> [configuration_options]

You can also create peer routing relationships between OVN networks in different projects:

    lxc network peer create <network1> <peering_name> <project2/network2> [configuration_options] --project=<project1>
    lxc network peer create <network2> <peering_name> <project1/network1> [configuration_options] --project=<project2>

```{important}
If the project or the network name is incorrect, the command will not return any error indicating that the respective project/network does not exist, and the routing relationship will remain in pending state.
This behavior prevents users in a different project from discovering whether a project and network exists.
```

### Peering properties

Peer routing relationships have the following properties:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group network-peering-peering-properties start -->
    :end-before: <!-- config group network-peering-peering-properties end -->
```

## List routing relationships

To list all network peerings for a network, use the following command:

    lxc network peer list <network>

## Edit a routing relationship

Use the following command to edit a network peering:

    lxc network peer edit <network> <peering_name>

This command opens the network peering in YAML format for editing.
