---
discourse: lxc:[How&#32;to&#32;use&#32;a&#32;second&#32;IP&#32;with&#32;a&#32;container&#32;and&#32;routed&#32;NIC](13021)
myst:
  html_meta:
    description: An index of how-to guides for LXD networking, including creating and configuring networks and network features such as ACLs, forwards, and BGP.
---

(networking)=
# Networking

These how-to guides cover common operations related to LXD networking.

## Create and configure networks

```{toctree}
:titlesonly:

Create a network </howto/network_create>
Configure a network </howto/network_configure>
```

## Configure networking features

These features are available for multiple types of networks.

```{toctree}
:titlesonly:

Configure as BGP server </howto/network_bgp>
Configure network ACLs </howto/network_acls>
Configure forwards </howto/network_forwards>
Configure network zones </howto/network_zones>
```

## Configure bridge network features

These features are available for managed bridge networks only.

```{toctree}
:titlesonly:

Configure your firewall </howto/network_bridge_firewalld>
Integrate with resolved </howto/network_bridge_resolved>
```

## Configure OVN network features

These features are available for OVN networks only.

```{toctree}
:titlesonly:

Set up OVN </howto/network_ovn_setup>
Configure load balancers </howto/network_load_balancers>
Configure peer routing </howto/network_ovn_peers>
```

## Troubleshoot networks

IPAM information shows the IP addresses allocated across networks and instances, useful for diagnosing network issues.

```{toctree}
:titlesonly:

Display IPAM information </howto/network_ipam>
```

## Related topics

{{networks_exp}}

{{networks_ref}}
