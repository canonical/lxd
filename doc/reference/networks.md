(ref-networks)=
# Networks

LXD supports different network types for {ref}`managed-networks`.

## Fully controlled networks

<!-- Include start controlled intro -->
Fully controlled networks create network interfaces and provide most functionality, including, for example, the ability to do IP management.

LXD supports the following network types:
<!-- Include end controlled intro -->

```{toctree}
:titlesonly:

network_bridge
network_ovn
```

## External networks

<!-- Include start external intro -->
External networks use network interfaces that already exist.
Therefore, LXD has limited possibility to control them, and LXD features like network ACLs, network forwards and network zones are not supported.

The main purpose for using external networks is to provide an uplink network through a parent interface.
This external network specifies the presets to use when connecting instances or other networks to a parent interface.

LXD supports the following external network types:
<!-- Include end external intro -->

```{toctree}
:titlesonly:

network_macvlan
network_physical
network_sriov
```

## Related topics

{{networks_how}}

{{networks_exp}}
