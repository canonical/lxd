(instances-configure)=
# How to configure instances

You can configure instances by setting {ref}`instance-options` or by adding and configuring {ref}`devices`.

See the following sections for instructions.

```{note}
To store and reuse different instance configurations, use {ref}`profiles <profiles>`.
```

## Configure instance options

You can specify instance options when you {ref}`create an instance <instances-create>`.

To update instance options after the instance is created, use the `lxc config set` command.
Specify the instance name and the key and value of the instance option:

    lxc config set <instance_name> <option_key>=<option_value> <option_key>=<option_value> ...

See {ref}`instance-options` for a list of available options and information about which options are available for which instance type.

For example, to change the memory limit for your container, enter the following command:

    lxc config set my-container limits.memory=128MiB

```{note}
Some of the instance options are updated immediately while the instance is running.
Others are updated only when the instance is restarted.

See the "Live update" column in the {ref}`instance-options` table for information about which options are applied immediately while the instance is running.
```

## Configure devices

To add and configure an instance device for your instance, use the `lxc config device add` command.
Specify the instance name, a device name, the device type and maybe device options (depending on the {ref}`device type <device-types>`):

    lxc config device add <instance_name> <device_name> <device_type> <device_option_key>=<device_option_value> <device_option_key>=<device_option_value> ...

See {ref}`devices` for a list of available device types and their options.

For example, to add the storage at `/share/c1` on the host system to your instance at path `/opt`, enter the following command:

    lxc config device add my-container disk-storage-device disk source=/share/c1 path=/opt

To configure instance device options for a device that you have added earlier, use the `lxc config device set` command:

    lxc config device set <instance_name> <device_name> <device_option_key>=<device_option_value> <device_option_key>=<device_option_value> ...

## Display instance configuration

To display the current configuration of your instance, including writable instance properties, instance options, devices and device options, enter the following command:

    lxc config show <instance_name> --expanded

(instances-configure-edit)=
## Edit the full instance configuration

To edit the full instance configuration, including writable instance properties, instance options, devices and device options, enter the following command:

    lxc config edit <instance_name>

```{note}
For convenience, the `lxc config edit` command displays the full configuration including read-only instance properties.
However, you cannot edit those properties.
Any changes are ignored.
```
