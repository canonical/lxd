(devices-tpm)=
# Type: `tpm`

```{note}
The `tpm` device type is supported for both containers and VMs.
It supports hotplugging only for containers, not for VMs.
```

TPM devices enable access to a TPM emulator.

## Device options

`tpm` devices have the following device options:

Key                 | Type      | Default   | Required       | Description
:--                 | :--       | :--       | :--            | :--
`path`              | string    | -         | for containers | Only for containers: path inside the instance (for example, `/dev/tpm0`)
`pathrm`            | string    | -         | for containers | Only for containers: resource manager path inside the instance (for example, `/dev/tpmrm0`)
