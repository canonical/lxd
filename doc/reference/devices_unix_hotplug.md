(devices-unix-hotplug)=
# Type: `unix-hotplug`

Supported instance types: container

Unix hotplug device entries make the requested Unix device appear in the
instance's `/dev` and allow read/write operations to it if the device exists on
the host system. Implementation depends on `systemd-udev` to be run on the host.

The following properties exist:

Key         | Type      | Default           | Required  | Description
:--         | :--       | :--               | :--       | :--
`vendorid`  | string    | -                 | no        | The vendor ID of the Unix device
`productid` | string    | -                 | no        | The product ID of the Unix device
`uid`       | int       | `0`               | no        | UID of the device owner in the instance
`gid`       | int       | `0`               | no        | GID of the device owner in the instance
`mode`      | int       | `0660`            | no        | Mode of the device in the instance
`required`  | bool      | `false`           | no        | Whether or not this device is required to start the instance. (The default is `false`, and all devices can be hotplugged)
