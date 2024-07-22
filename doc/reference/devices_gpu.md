(devices-gpu)=
# Type: `gpu`

```{youtube} https://www.youtube.com/watch?v=T0aV2LsMpoA
```

GPU devices make the specified GPU device or devices appear in the instance.

```{note}
For containers, a `gpu` device may match multiple GPUs at once.
For VMs, each device can match only a single GPU.
```

The following types of GPUs can be added using the `gputype` device option:

- [`physical`](gpu-physical) (container and VM): Passes an entire GPU through into the instance.
  This value is the default if `gputype` is unspecified.
- [`mdev`](gpu-mdev) (VM only): Creates and passes a virtual GPU through into the instance.
- [`mig`](gpu-mig) (container only): Creates and passes a MIG (Multi-Instance GPU) through into the instance.
- [`sriov`](gpu-sriov) (VM only): Passes a virtual function of an SR-IOV-enabled GPU into the instance.

The available device options depend on the GPU type and are listed in the tables in the following sections.

(gpu-physical)=
## `gputype`: `physical`

```{note}
The `physical` GPU type is supported for both containers and VMs.
It supports hotplugging only for containers, not for VMs.
```

A `physical` GPU device passes an entire GPU through into the instance.

### Device options

GPU devices of type `physical` have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-gpu-physical-device-conf start -->
    :end-before: <!-- config group device-gpu-physical-device-conf end -->
```

### Configuration examples

Add all GPUs from the host system as a `physical` GPU device to an instance:

    lxc config device add <instance_name> <device_name> gpu gputype=physical

Add a specific GPU from the host system as a `physical` GPU device to an instance by specifying its PCI address:

    lxc config device add <instance_name> <device_name> gpu gputype=physical pci=<pci_address>

See {ref}`instances-configure-devices` for more information.

(gpu-mdev)=
## `gputype`: `mdev`

```{note}
The `mdev` GPU type is supported only for VMs.
It does not support hotplugging.
```

An `mdev` GPU device creates and passes a virtual GPU through into the instance.
You can check the list of available `mdev` profiles by running [`lxc info --resources`](lxc_info.md).

### Device options

GPU devices of type `mdev` have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-gpu-mdev-device-conf start -->
    :end-before: <!-- config group device-gpu-mdev-device-conf end -->
```

### Configuration examples

Add an `mdev` GPU device to an instance by specifying its `mdev` profile and the PCI address of the GPU:

    lxc config device add <instance_name> <device_name> gpu gputype=mdev mdev=<mdev_profile> pci=<pci_address>

See {ref}`instances-configure-devices` for more information.

(gpu-mig)=
## `gputype`: `mig`

```{note}
The `mig` GPU type is supported only for containers.
It does not support hotplugging.
```

A `mig` GPU device creates and passes a MIG compute instance through into the instance.
Currently, this requires NVIDIA MIG instances to be pre-created.

### Device options

GPU devices of type `mig` have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-gpu-mig-device-conf start -->
    :end-before: <!-- config group device-gpu-mig-device-conf end -->
```

You must set either {config:option}`device-gpu-mig-device-conf:mig.uuid` (NVIDIA drivers 470+) or both {config:option}`device-gpu-mig-device-conf:mig.ci` and {config:option}`device-gpu-mig-device-conf:mig.gi` (old NVIDIA drivers).

### Configuration examples

Add a `mig` GPU device to an instance by specifying its UUID and the PCI address of the GPU:

    lxc config device add <instance_name> <device_name> gpu gputype=mig mig.uuid=<mig_uuid> pci=<pci_address>

See {ref}`instances-configure-devices` for more information.

(gpu-sriov)=
## `gputype`: `sriov`

```{note}
The `sriov` GPU type is supported only for VMs.
It does not support hotplugging.
```

An `sriov` GPU device passes a virtual function of an SR-IOV-enabled GPU into the instance.

### Device options

GPU devices of type `sriov` have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-gpu-sriov-device-conf start -->
    :end-before: <!-- config group device-gpu-sriov-device-conf end -->
```

### Configuration examples

Add a `sriov` GPU device to an instance by specifying the PCI address of the parent GPU:

    lxc config device add <instance_name> <device_name> gpu gputype=sriov pci=<pci_address>

See {ref}`instances-configure-devices` for more information.
