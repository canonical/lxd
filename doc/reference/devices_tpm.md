(devices-tpm)=
# Type: `tpm`

```{youtube} https://www.youtube.com/watch?v=iE1TN7YIqP0
:title: LXD TPM devices
```

```{note}
The `tpm` device type is supported for both containers and VMs.
It supports hotplugging only for containers, not for VMs.
```

TPM devices enable access to a {abbr}`TPM (Trusted Platform Module)` emulator.

TPM devices can be used to validate the boot process and ensure that no steps in the boot chain have been tampered with, and they can securely generate and store encryption keys.

LXD uses a software TPM that supports TPM 2.0.
For containers, the main use case is sealing certificates, which means that the keys are stored outside of the container, making it virtually impossible for attackers to retrieve them.
For virtual machines, TPM can be used both for sealing certificates and for validating the boot process, which allows using full disk encryption compatible with, for example, Windows BitLocker.

## Device options

`tpm` devices have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-tpm-device-conf start -->
    :end-before: <!-- config group device-tpm-device-conf end -->
```

## Configuration examples

Add a `tpm` device to a container by specifying its path and the resource manager path:

    lxc config device add <instance_name> <device_name> tpm path=<path_on_instance> pathrm=<resource_manager_path>

Add a `tpm` device to a virtual machine:

    lxc config device add <instance_name> <device_name> tpm

See {ref}`instances-configure-devices` for more information.
