(devices-unix-char)=
# Type: `unix-char`

Supported instance types: container

Unix character device entries simply make the requested character device
appear in the instance's `/dev` and allow read/write operations to it.

The following properties exist:

Key         | Type      | Default           | Required  | Description
:--         | :--       | :--               | :--       | :--
`source`    | string    | -                 | no        | Path on the host
`path`      | string    | -                 | no        | Path inside the instance (one of `source` and `path` must be set)
`major`     | int       | device on host    | no        | Device major number
`minor`     | int       | device on host    | no        | Device minor number
`uid`       | int       | `0`               | no        | UID of the device owner in the instance
`gid`       | int       | `0`               | no        | GID of the device owner in the instance
`mode`      | int       | `0660`            | no        | Mode of the device in the instance
`required`  | bool      | `true`            | no        | Whether or not this device is required to start the instance
