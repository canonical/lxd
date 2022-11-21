(devices-tpm)=
# Type: `tpm`

Supported instance types: container, VM

TPM device entries enable access to a TPM emulator.

The following properties exist:

Key                 | Type      | Default   | Required  | Description
:--                 | :--       | :--       | :--       | :--
`path`              | string    | -         | yes       | Path inside the instance (only for containers). E.g. `/dev/tpm0`
`pathrm`            | string    | -         | yes       | Resource manager path inside the instance (only for containers). E.g. `/dev/tpmrm0`
