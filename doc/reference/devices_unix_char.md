(devices-unix-char)=
# Type: `unix-char`

```{youtube} https://www.youtube.com/watch?v=C2e3LD5wLI8
:title: LXD Unix devices - YouTube
```

```{note}
The `unix-char` device type is supported for containers.
It supports hotplugging.
```

Unix character devices make the specified character device appear as a device in the instance (under `/dev`).
You can read from the device and write to it.

## Device options

`unix-char` devices have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-unix-char-device-conf start -->
    :end-before: <!-- config group device-unix-char-device-conf end -->
```

## Configuration examples

Add a `unix-char` device to an instance by specifying its source and path:

    lxc config device add <instance_name> <device_name> unix-char source=<path_on_host> path=<path_on_instance>

If you want to use the same path on the instance as on the host, you can omit the `source` option:

    lxc config device add <instance_name> <device_name> unix-char path=<path_to_the_device>

See {ref}`instances-configure-devices` for more information.

(devices-unix-char-hotplugging)=
## Hotplugging

% Include content from [devices_unix_block.md](device_unix_block.md)
```{include} devices_unix_block.md
    :start-after: Hotplugging
```
