(devices-usb)=
# Type: `usb`

```{note}
The `usb` device type is supported for both containers and VMs.
It supports hotplugging for both containers and VMs.
```

USB devices make the specified USB device appear in the instance.

## Device options

`usb` devices have the following device options:

Key         | Type      | Default           | Description
:--         | :--       | :--               | :--
`gid`       | int       | `0`               | GID of the device owner in the instance
`mode`      | int       | `0660`            | Mode of the device in the instance
`productid` | string    | -                 | The product ID of the USB device
`required`  | bool      | `false`           | Whether this device is required to start the instance (the default is `false`, and all devices can be hotplugged)
`uid`       | int       | `0`               | UID of the device owner in the instance
`vendorid`  | string    | -                 | The vendor ID of the USB device
