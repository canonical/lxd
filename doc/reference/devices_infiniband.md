(devices-infiniband)=
# Type: `infiniband`

Supported instance types: container, VM

LXD supports two different kind of network types for InfiniBand devices:

- `physical`: Straight physical device pass-through from the host. The targeted device will vanish from the host and appear in the instance.
- `sriov`: Passes a virtual function of an SR-IOV enabled physical network device into the instance.

Different network interface types have different additional properties, the current list is:

Key                     | Type      | Default           | Required  | Used by             | Description
:--                     | :--       | :--               | :--       | :--                 | :--
`nictype`               | string    | -                 | yes       | all                 | The device type, one of `physical` or `sriov`
`name`                  | string    | kernel assigned   | no        | all                 | The name of the interface inside the instance
`hwaddr`                | string    | randomly assigned | no        | all                 | The MAC address of the new interface. Can be either full 20 byte variant or short 8 byte variant (which will only modify the last 8 bytes of the parent device)
`mtu`                   | integer   | parent MTU        | no        | all                 | The MTU of the new interface
`parent`                | string    | -                 | yes       | `physical`, `sriov` | The name of the host device or bridge

To create a `physical` `infiniband` device use:

```
lxc config device add <instance> <device-name> infiniband nictype=physical parent=<device>
```

## SR-IOV with InfiniBand devices

InfiniBand devices do support SR-IOV but in contrast to other SR-IOV enabled
devices InfiniBand does not support dynamic device creation in SR-IOV mode.
This means users need to pre-configure the number of virtual functions by
configuring the corresponding kernel module.

To create a `sriov` `infiniband` device use:

```
lxc config device add <instance> <device-name> infiniband nictype=sriov parent=<sriov-enabled-device>
```
