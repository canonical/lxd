---
myst:
  html_meta:
    description: Reference information for LXD network types, covering fully controlled networks (bridge, OVN) and external networks (macvlan, physical, SR-IOV).
---

(ref-networks)=
# Networks

LXD supports different network types for {ref}`managed-networks`.

## Fully controlled networks

<!-- Include start controlled intro -->
Fully controlled networks create and manage their own network interfaces, supporting features like IP management and network ACLs, forwards, and zones.

LXD supports the following network types:
<!-- Include end controlled intro -->

```{toctree}
:titlesonly:

network_bridge
network_ovn
```

## External networks

<!-- Include start external intro -->
External networks use interfaces that already exist. As a result, LXD has limited control over them, and LXD networking features like ACLs, forwards, and zones are not supported.

External networks mainly serve as uplink networks, providing a parent interface for connecting instances or other networks. They also specify the configuration presets applied when making those connections.

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
