(network-sriov)=
# SR-IOV network

<!-- Include start SR-IOV intro -->
{abbr}`SR-IOV (Single root I/O virtualization)` is a hardware standard that allows a single network card port to appear as several virtual network interfaces in a virtualized environment.
<!-- Include end SR-IOV intro -->

The `sriov` network type allows to specify presets to use when connecting instances to a parent interface.
In this case, the instance NICs can simply set the `network` option to the network they connect to without knowing any of the underlying configuration details.

(network-sriov-options)=
## Configuration options

The following configuration key namespaces are currently supported for the `sriov` network type:

- `maas` (MAAS network identification)
- `user` (free-form key/value for user metadata)

```{note}
{{note_ip_addresses_CIDR}}
```

The following configuration options are available for the `sriov` network type:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group network-sriov-network-conf start -->
    :end-before: <!-- config group network-sriov-network-conf end -->
```
