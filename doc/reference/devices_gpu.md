(devices-gpu)=
# Type: `gpu`

```{youtube} https://www.youtube.com/watch?v=T0aV2LsMpoA
```

GPU device entries simply make the requested GPU device appear in the
instance.

```{note}
Container devices may match multiple GPUs at once. However, for virtual machines a device can only match a single GPU.
```

## GPUs available

The following GPUs can be specified using the `gputype` property:

- [`physical`](#gpu-physical) Passes through an entire GPU. This is the default if `gputype` is unspecified.
- [`mdev`](#gpu-mdev) Creates and passes through a virtual GPU into the instance.
- [`mig`](#gpu-mig) Creates and passes through a MIG (Multi-Instance GPU) device into the instance.
- [`sriov`](#gpu-sriov) Passes a virtual function of an SR-IOV enabled GPU into the instance.

## `gpu`: `physical`

Supported instance types: container, VM

Passes through an entire GPU.

The following properties exist:

Key         | Type      | Default           | Required  | Description
:--         | :--       | :--               | :--       | :--
`vendorid`  | string    | -                 | no        | The vendor ID of the GPU device
`productid` | string    | -                 | no        | The product ID of the GPU device
`id`        | string    | -                 | no        | The card ID of the GPU device
`pci`       | string    | -                 | no        | The PCI address of the GPU device
`uid`       | int       | `0`               | no        | UID of the device owner in the instance (container only)
`gid`       | int       | `0`               | no        | GID of the device owner in the instance (container only)
`mode`      | int       | `0660`            | no        | Mode of the device in the instance (container only)

## `gpu`: `mdev`

Supported instance types: VM

Creates and passes through a virtual GPU into the instance. A list of available `mdev` profiles can be found by running `lxc info --resources`.

The following properties exist:

Key         | Type      | Default           | Required  | Description
:--         | :--       | :--               | :--       | :--
`vendorid`  | string    | -                 | no        | The vendor ID of the GPU device
`productid` | string    | -                 | no        | The product ID of the GPU device
`id`        | string    | -                 | no        | The card ID of the GPU device
`pci`       | string    | -                 | no        | The PCI address of the GPU device
`mdev`      | string    | -                 | yes       | The `mdev` profile to use (e.g. `i915-GVTg_V5_4`)

## `gpu`: `mig`

Supported instance types: container

Creates and passes through a MIG compute instance. This currently requires NVIDIA MIG instances to be pre-created.

The following properties exist:

Key         | Type      | Default           | Required  | Description
:--         | :--       | :--               | :--       | :--
`vendorid`  | string    | -                 | no        | The vendor ID of the GPU device
`productid` | string    | -                 | no        | The product ID of the GPU device
`id`        | string    | -                 | no        | The card ID of the GPU device
`pci`       | string    | -                 | no        | The PCI address of the GPU device
`mig.ci`    | int       | -                 | no        | Existing MIG compute instance ID
`mig.gi`    | int       | -                 | no        | Existing MIG GPU instance ID
`mig.uuid`  | string    | -                 | no        | Existing MIG device UUID (`MIG-` prefix can be omitted)

Note: Either `mig.uuid` (NVIDIA drivers 470+) or both `mig.ci` and `mig.gi` (old NVIDIA drivers) must be set.

## `gpu`: `sriov`

Supported instance types: VM

Passes a virtual function of an SR-IOV enabled GPU into the instance.

The following properties exist:

Key         | Type      | Default           | Required  | Description
:--         | :--       | :--               | :--       | :--
`vendorid`   | string   | -                 | no        | The vendor ID of the parent GPU device
`productid`  | string   | -                 | no        | The product ID of the parent GPU device
`id`         | string   | -                 | no        | The card ID of the parent GPU device
`pci`        | string   | -                 | no        | The PCI address of the parent GPU device
