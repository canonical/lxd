---
discourse: ubuntu:42313
---

# UEFI variables for VMs

{abbr}`UEFI (Unified Extensible Firmware Interface)` variables store and represent configuration settings of the UEFI firmware.
See [UEFI](https://en.wikipedia.org/wiki/UEFI) for more information.

You can see a list of UEFI variables on your system by running `ls -l /sys/firmware/efi/efivars/`.
Usually, you don't need to touch these variables, but in specific cases they can be useful to debug UEFI, SHIM, or boot loader issues in virtual machines.

To configure UEFI variables for a VM, use the [`lxc config uefi`](lxc_config_uefi.md) command or the `/1.0/instances/<instance_name>/uefi-vars` endpoint.

For example, to set a variable to a value (hexadecimal):

````{tabs}
```{group-tab} CLI
    lxc config uefi set <instance_name> <variable_name>-<GUID>=<value>
```
```{group-tab} API
    lxc query --request PUT /1.0/instances/<instance_name>/uefi-vars --data '{
      "variables": {
        "<variable_name>-<GUID>": {
          "attr": 3,
          "data": "<value>"
        },
      }
    }'

See [`PUT /1.0/instances/{name}/uefi-vars`](swagger:/instances/instance_uefi_vars_put) for more information.
```
````

To display the variables that are set for a specific VM:

````{tabs}
```{group-tab} CLI
    lxc config uefi show <instance_name>
```
```{group-tab} API
    lxc query --request GET /1.0/instances/<instance_name>/uefi-vars

See [`GET /1.0/instances/{name}/uefi-vars`](swagger:/instances/instance_uefi_vars_get) for more information.
```
````

## Example

You can use UEFI variables to disable secure boot, for example.

```{important}
Use this method only for debugging purposes.
LXD provides the {config:option}`instance-security:security.secureboot` option to control the secure boot behavior.
```

The following command checks the secure boot state:

    lxc config uefi get v1 SecureBootEnable-f0a30bc7-af08-4556-99c4-001009c93a44

A value of `01` indicates that secure boot is active.
You can then turn it off with the following command:

    lxc config uefi set v1 SecureBootEnable-f0a30bc7-af08-4556-99c4-001009c93a44=00
