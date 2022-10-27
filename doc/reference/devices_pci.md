(devices-pci)=
# Type: `pci`

Supported instance types: VM

PCI device entries are used to pass raw PCI devices from the host into a virtual machine.

The following properties exist:

Key                 | Type      | Default   | Required  | Description
:--                 | :--       | :--       | :--       | :--
`address`           | string    | -         | yes       | PCI address of the device.
