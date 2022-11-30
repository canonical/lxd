(devices-usb)=
# Type: `usb`

Supported instance types: container, VM (supports hotplugging)

USB device entries simply make the requested USB device appear in the
instance.

The following properties exist:

Key         | Type      | Default           | Required  | Description
:--         | :--       | :--               | :--       | :--
`gid`       | int       | `0`               | no        | GID of the device owner in the instance
`mode`      | int       | `0660`            | no        | Mode of the device in the instance
`productid` | string    | -                 | no        | The product ID of the USB device
`required`  | bool      | `false`           | no        | Whether or not this device is required to start the instance. (The default is `false`, and all devices can be hotplugged)
`uid`       | int       | `0`               | no        | UID of the device owner in the instance
`vendorid`  | string    | -                 | no        | The vendor ID of the USB device
