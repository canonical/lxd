(devices-pci)=
# Type: `pci`

```{note}
The `pci` device type is supported for VMs.
It does not support hotplugging.
```

PCI devices are used to pass raw PCI devices from the host into a virtual machine.

## Device options

`pci` devices have the following device options:

Key                 | Type      | Default   | Required  | Description
:--                 | :--       | :--       | :--       | :--
`address`           | string    | -         | yes       | PCI address of the device
