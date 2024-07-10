(devices-pci)=
# Type: `pci`

```{youtube} https://www.youtube.com/watch?v=h3DZXbmsZHg
:title: LXD PCI devices
```

```{note}
The `pci` device type is supported for VMs.
It does not support hotplugging.
```

PCI devices are used to pass raw PCI devices from the host into a virtual machine.

They are mainly intended to be used for specialized single-function PCI cards like sound cards or video capture cards.
In theory, you can also use them for more advanced PCI devices like GPUs or network cards, but it's usually more convenient to use the specific device types that LXD provides for these devices ([`gpu` device](devices-gpu) or [`nic` device](devices-nic)).

## Device options

`pci` devices have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-pci-device-conf start -->
    :end-before: <!-- config group device-pci-device-conf end -->
```

## Configuration examples

Add a `pci` device to a virtual machine by specifying its PCI address:

    lxc config device add <instance_name> <device_name> pci address=<pci_address>

To determine the PCI address, you can use {command}`lspci`, for example.

See {ref}`instances-configure-devices` for more information.
