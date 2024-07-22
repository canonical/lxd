(devices-infiniband)=
# Type: `infiniband`

```{youtube} https://www.youtube.com/watch?v=SDewhlRSOuM
:title: LXD InfiniBand devices - YouTube
```

```{note}
The `infiniband` device type is supported for both containers and VMs.
It supports hotplugging only for containers, not for VMs.
```

LXD supports two different kinds of network types for InfiniBand devices:

- `physical`: Passes a physical device from the host through to the instance.
  The targeted device will vanish from the host and appear in the instance.
- `sriov`: Passes a virtual function of an SR-IOV-enabled physical network device into the instance.

  ```{note}
  InfiniBand devices support SR-IOV, but in contrast to other SR-IOV-enabled devices, InfiniBand does not support dynamic device creation in SR-IOV mode.
  Therefore, you must pre-configure the number of virtual functions by configuring the corresponding kernel module.
  ```

## Device options

`infiniband` devices have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-infiniband-device-conf start -->
    :end-before: <!-- config group device-infiniband-device-conf end -->
```

## Configuration examples

Add a `physical` `infiniband` device to an instance:

    lxc config device add <instance_name> <device_name> infiniband nictype=physical parent=<device>

Add an `sriov` `infiniband` device to an instance:

    lxc config device add <instance_name> <device_name> infiniband nictype=sriov parent=<sriov_enabled_device>

See {ref}`instances-configure-devices` for more information.
