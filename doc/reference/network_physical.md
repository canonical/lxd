(network-physical)=
# Physical network

<!-- Include start physical intro -->
The `physical` network type connects to an existing physical network, which can be a network interface or a bridge, and serves as an uplink network for OVN.
<!-- Include end physical intro -->

This network type allows to specify presets to use when connecting OVN networks to a parent interface or to allow an instance to use a physical interface as a NIC.
In this case, the instance NICs can simply set the `network`option to the network they connect to without knowing any of the underlying configuration details.

(network-physical-options)=
## Configuration options

The following configuration key namespaces are currently supported for the `physical` network type:

- `bgp` (BGP peer configuration)
- `dns` (DNS server and resolution configuration)
- `ipv4` (L3 IPv4 configuration)
- `ipv6` (L3 IPv6 configuration)
- `maas` (MAAS network identification)
- `ovn` (OVN configuration)
- `user` (free-form key/value for user metadata)

```{note}
{{note_ip_addresses_CIDR}}
```

The following configuration options are available for the `physical` network type:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group network-physical-network-conf start -->
    :end-before: <!-- config group network-physical-network-conf end -->
```

(network-physical-features)=
## Supported features

The following features are supported for the `physical` network type:

- {ref}`network-bgp`
