(profiles)=
# How to use profiles

Profiles store a set of configuration options.
They can contain instance options, devices and device options.

You can apply any number of profiles to an instance.
They are applied in the order they are specified, so the last profile to specify a specific key takes precedence.
However, instance-specific configuration always overrides the configuration coming from the profiles.

```{note}
Profiles can be applied to containers and virtual machines.
Therefore, they might contain options and devices that are valid for either type.

When applying a profile that contains configuration that is not suitable for the instance type, this configuration is ignored and does not result in an error.
```

If you don't specify any profiles when launching a new instance, the `default` profile is applied automatically.
This profile defines a network interface and a root disk.
The `default` profile cannot be renamed or removed.

## View profiles

Enter the following command to display a list of all available profiles:

    lxc profile list

Enter the following command to display the contents of a profile:

    lxc profile show <profile_name>

## Create an empty profile

Enter the following command to create an empty profile:

    lxc profile create <profile_name>

## Edit a profile

You can either set specific configuration options for a profile or edit the full profile in YAML format.

### Set specific options for a profile

To set an instance option for a profile, use the `lxc profile set` command.
Specify the profile name and the key and value of the instance option:

    lxc profile set <profile_name> <option_key>=<option_value> <option_key>=<option_value> ...

To add and configure an instance device for your profile, use the `lxc profile device add` command.
Specify the profile name, a device name, the device type and maybe device options (depending on the {ref}`device type <device-types>`):

    lxc profile device add <instance_name> <device_name> <device_type> <device_option_key>=<device_option_value> <device_option_key>=<device_option_value> ...

To configure instance device options for a device that you have added to the profile earlier, use the `lxc profile device set` command:

    lxc profile device set <instance_name> <device_name> <device_option_key>=<device_option_value> <device_option_key>=<device_option_value> ...

### Edit the full profile

Instead of setting each configuration option separately, you can provide all options at once in YAML format.

Check the contents of an existing profile or instance configuration for the required markup.
For example, the `default` profile might look like this:

    config: {}
    description: Default LXD profile
    devices:
      eth0:
        name: eth0
        network: lxdbr0
        type: nic
      root:
        path: /
        pool: default
        type: disk
    name: default
    used_by:

Instance options are provided as an array under `config`.
Instance devices and instance device options are provided under `devices`.

To edit a profile using your standard terminal editor, enter the following command:

    lxc profile edit <profile_name>

Alternatively, you can create a YAML file (for example, `profile.yaml`) with the configuration and write the configuration to the profile with the following command:

    lxc profile edit <profile_name> < profile.yaml

## Apply a profile to an instance

Enter the following command to apply a profile to an instance:

    lxc profile add <instance_name> <profile_name>

```{tip}
Check the configuration after adding the profile: `lxc config show <instance_name>`

You will see that your profile is now listed under `profiles`.
However, the configuration options from the profile are not shown under `config` (unless you add the `--expanded` flag).
The reason for this behavior is that these options are taken from the profile and not the configuration of the instance.

This means that if you edit a profile, the changes are automatically applied to all instances that use the profile.
```

You can also specify profiles when launching an instance by adding the `--profile` flag:

    lxc launch <image> <instance_name> --profile <profile> --profile <profile> ...

## Remove a profile from an instance

Enter the following command to remove a profile from an instance:

    lxc profile remove <instance_name> <profile_name>
