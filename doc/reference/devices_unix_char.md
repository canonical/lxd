(devices-unix-char)=
# Type: `unix-char`

Supported instance types: container

Unix character device entries simply make the requested character device
appear in the instance's `/dev` and allow read/write operations to it.

The following properties exist:

Key         | Type      | Default           | Required  | Description
:--         | :--       | :--               | :--       | :--
`gid`       | int       | `0`               | no        | GID of the device owner in the instance
`major`     | int       | device on host    | no        | Device major number
`minor`     | int       | device on host    | no        | Device minor number
`mode`      | int       | `0660`            | no        | Mode of the device in the instance
`path`      | string    | -                 | no        | Path inside the instance (one of `source` and `path` must be set)
`required`  | bool      | `true`            | no        | Whether or not this device is required to start the instance
`source`    | string    | -                 | no        | Path on the host
`uid`       | int       | `0`               | no        | UID of the device owner in the instance
