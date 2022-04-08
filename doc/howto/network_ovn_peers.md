---
discourse: 12165
---

(network-ovn-peers)=
# How to create peer routing relationships

Network peers allow the creation of routing relationships between two OVN networks.
This allows for traffic between those two networks to stay within the OVN subsystem rather than having to transit
via the uplink network.

Both networks in the peering are required to complete a setup step to ensure that the peering is mutual.

E.g.

```
lxc network peer create <local_network> foo <target_project/target_network> --project=local_network
lxc network peer create <target_network> foo <local_project/local_network> --project=target_project
```

If either the project or network name specified in the peer setup step is incorrect, the user will not get an error
from the command explaining that the respective project/network does not exist. This is to prevent a user in a
different project from being able to discover whether a project and network exists.

## Properties
The following are network peer properties:

Property         | Type       | Required | Description
:--              | :--        | :--      | :--
name             | string     | yes      | Name of the Network Peer on the local network
description      | string     | no       | Description of Network Peer
config           | string set | no       | Config key/value pairs (Only `user.*` custom keys supported)
ports            | port list  | no       | Network forward port list
target_project   | string     | yes      | Which project the target network exists in (required at create time).
target_network   | string     | yes      | Which network to create a peer with (required at create time).
status           | string     | --       | Status indicates if pending or created (mutual peering exists with the target network).
