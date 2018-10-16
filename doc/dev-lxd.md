# Communication between container and host
## Introduction
Communication between the hosted workload (container) and its host while
not strictly needed is a pretty useful feature.

In LXD, this feature is implemented through a `/dev/lxd/sock` node which is
created and setup for all LXD containers.

This file is a Unix socket which processes inside the container can
connect to. It's multi-threaded so multiple clients can be connected at the
same time.

## Implementation details
LXD on the host binds `/var/lib/lxd/devlxd` and starts listening for new
connections on it.

This socket is then bind-mounted into every single container started by
LXD at `/dev/lxd/sock`.

The bind-mount is required so we can exceed 4096 containers, otherwise,
LXD would have to bind a different socket for every container, quickly
reaching the FD limit.

## Authentication
Queries on `/dev/lxd/sock` will only return information related to the
requesting container. To figure out where a request comes from, LXD will
extract the initial socket ucred and compare that to the list of
containers it manages.

## Protocol
The protocol on `/dev/lxd/sock` is plain-text HTTP with JSON messaging, so very
similar to the local version of the LXD protocol.

Unlike the main LXD API, there is no background operation and no
authentication support in the `/dev/lxd/sock` API.

## REST-API
### API structure
 * /
   * /1.0
     * /1.0/config
       * /1.0/config/{key}
     * /1.0/events
     * /1.0/images/{fingerprint}/export
     * /1.0/meta-data

### API details
#### `/`
##### GET
 * Description: List of supported APIs
 * Return: list of supported API endpoint URLs (by default `['/1.0']`)

Return value:

```json
[
    "/1.0"
]
```
#### `/1.0`
##### GET
 * Description: Information about the 1.0 API
 * Return: dict

Return value:

```json
{
    "api_version": "1.0"
}
```
#### `/1.0/config`
##### GET
 * Description: List of configuration keys
 * Return: list of configuration keys URL

Note that the configuration key names match those in the container
config, however not all configuration namespaces will be exported to
`/dev/lxd/sock`.
Currently only the `user.*` keys are accessible to the container.

At this time, there also aren't any container-writable namespace.

Return value:

```json
[
    "/1.0/config/user.a"
]
```

#### `/1.0/config/<KEY>`
##### GET
 * Description: Value of that key
 * Return: Plain-text value

Return value:

    blah

#### `/1.0/events`
##### GET
 * Description: websocket upgrade
 * Return: none (never ending flow of events)

Supported arguments are:

 * type: comma separated list of notifications to subscribe to (defaults to all)

The notification types are:

 * config (changes to any of the user.\* config keys)
 * device (any device addition, change or removal)

This never returns. Each notification is sent as a separate JSON dict:

    {
        "timestamp": "2017-12-21T18:28:26.846603815-05:00",
        "type": "device",
        "metadata": {
            "name": "kvm",
            "action": "added",
            "config": {
                "type": "unix-char",
                "path": "/dev/kvm"
            }
        }
    }

    {
        "timestamp": "2017-12-21T18:28:26.846603815-05:00",
        "type": "config",
        "metadata": {
            "key": "user.foo",
            "old_value": "",
            "value": "bar"
        }
    }

#### `/1.0/images/<FINGERPRINT>/export`
##### GET
 * Description: Download a public/cached image from the host
 * Return: raw image or error
 * Access: Requires security.devlxd.images set to true

Return value:

    See /1.0/images/<FINGERPRINT>/export in the daemon API.


#### `/1.0/meta-data`
##### GET
 * Description: Container meta-data compatible with cloud-init
 * Return: cloud-init meta-data

Return value:

    #cloud-config
    instance-id: abc
    local-hostname: abc
