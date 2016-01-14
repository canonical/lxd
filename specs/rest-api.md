# Introduction
All the communications between LXD and its clients happen using a
RESTful API over http which is then encapsulated over either SSL for
remote operations or a unix socket for local operations.

Not all of the REST interface requires authentication:

 * GET to / is allowed for everyone (lists the API endpoints)
 * GET to /1.0 is allowed for everyone (but result varies)
 * POST to /1.0/certificates is allowed for everyone with a client certificate
 * GET to /1.0/images/\* is allowed for everyone but only returns public images for unauthenticated users

Unauthenticated endpoints are clearly identified as such below.

# API versioning
The list of supported major API versions can be retrieved using GET /.

The reason for a major API bump is if the API breaks backward compatibility.

Feature additions done without breaking backward compatibility only
result in a bump of the compat version which can be used by the client
to check if a given feature is supported by the server.

# Return values
There are three standard return types:
 * Standard return value
 * Background operation
 * Error

### Standard return value
For a standard synchronous operation, the following dict is returned:

    {
        'type': "sync",
        'status': "Success",
        'status_code': 200,
        'metadata': {}                          # Extra resource/action specific metadata
    }

HTTP code must be 200.

### Background operation
When a request results in a background operation, the HTTP code is set to 202 (Accepted)
and the Location HTTP header is set to the operation URL.

The body is a dict with the following structure:

    {
        'type': "async",
        'status': "OK",
        'status_code': 100,
        'operation': "/1.0/containers/<id>",                    # URL to the background operation
        'metadata': {}                                          # Operation metadata (see below)
    }

The operation metadata structure looks like:

    {
        "id": "a40f5541-5e98-454f-b3b6-8a51ef5dbd3c",           # UUID of the operation
        "class": "websocket",                                   # Class of the operation (task, websocket or token)
        "created_at": "2015-11-17T22:32:02.226176091-05:00",    # When the operation was created
        "updated_at": "2015-11-17T22:32:02.226176091-05:00",    # Last time the operation was updated
        "status": "Running",                                    # String version of the operation's status
        "status_code": 103,                                     # Integer version of the operation's status (use this rather than status)
        "resources": {                                          # Dictionary of resource types (container, snapshots, images) and affected resources
          "containers": [
            "/1.0/containers/test"
          ]
        },
        "metadata": {                                           # Metadata specific to the operation in question (in this case, exec)
          "fds": {
            "0": "2a4a97af81529f6608dca31f03a7b7e47acc0b8dc6514496eb25e325f9e4fa6a",
            "control": "5b64c661ef313b423b5317ba9cb6410e40b705806c28255f601c0ef603f079a7"
          }
        },
        "may_cancel": false,                                    # Whether the operation can be canceled (DELETE over REST)
        "err": ""                                               # The error string should the operation have failed
    }

The body is mostly provided as a user friendly way of seeing what's
going on without having to pull the target operation, all information in
the body can also be retrieved from the background operation URL.

### Error
There are various situations in which something may immediately go
wrong, in those cases, the following return value is used:

    {
        'type': "error",
        'error': "Failure",
        'error_code': 400,
        'metadata': {}                      # More details about the error
    }

HTTP code must be one of of 400, 401, 403, 404, 409, 412 or 500.

# Status codes
The LXD REST API often has to return status information, be that the
reason for an error, the current state of an operation or the state of
the various resources it exports.

To make it simple to debug, all of those are always doubled. There is a
numeric representation of the state which is guaranteed never to change
and can be relied on by API clients. Then there is a text version meant
to make it easier for people manually using the API to figure out what's
happening.

In most cases, those will be called status and status\_code, the former
being the user-friendly string representation and the latter the fixed
numeric value.

The codes are always 3 digits, with the following ranges:
 * 100 to 199: resource state (started, stopped, ready, ...)
 * 200 to 399: positive action result
 * 400 to 599: negative action result
 * 600 to 999: future use

## List of current status codes

Code  | Meaning
:---  | :------
100   | Operation created
101   | Started
102   | Stopped
103   | Running
104   | Cancelling
105   | Pending
106   | Starting
107   | Stopping
108   | Aborting
109   | Freezing
110   | Frozen
111   | Thawed
200   | Success
400   | Failure
401   | Cancelled


# Safety for concurrent updates
The API uses the HTTP ETAG to prevent potential problems when a resource
changes on the server between the time it was accessed by the client and
the time it is sent back for update.

All GET queries come with an Etag HTTP header which is a short hash of
the content that is relevant for an update. Any information which is
read-only, shouldn't be included in the hash.

On update (PUT), the same Etag field can be set by the client in its
request alongside a If-Match header.. If it's set, the server will
then compute the current Etag for the resource and compare the two.
The update will then only be done if the two match.
If they don't, an error will be returned instead using HTTP error code
412 (Precondition failed).

For consistency in LXD's use of hashes, the Etag hash should be a SHA-256.

# Recursion
To optimize queries of large lists, recursion is implemented for collections.
A "recursion" argument can be passed to a GET query against a collection.

The default value is 0 which means that collection member URLs are
returned. Setting it to 1 will have those URLs be replaced by the object
they point to (typically a dict).

# API structure
 * /
   * /1.0
     * /1.0/certificates
       * /1.0/certificates/\<fingerprint\>
     * /1.0/containers
       * /1.0/containers/\<name\>
         * /1.0/containers/\<name\>/exec
         * /1.0/containers/\<name\>/files
         * /1.0/containers/\<name\>/snapshots
         * /1.0/containers/\<name\>/snapshots/\<name\>
         * /1.0/containers/\<name\>/state
         * /1.0/containers/\<name\>/logs
         * /1.0/containers/\<name\>/logs/\<logfile\>
     * /1.0/events
     * /1.0/images
       * /1.0/images/\<fingerprint\>
         * /1.0/images/\<fingerprint\>/export
       * /1.0/images/aliases
         * /1.0/images/aliases/\<name\>
     * /1.0/networks
       * /1.0/networks/\<name\>
     * /1.0/operations
       * /1.0/operations/\<uuid\>
         * /1.0/operations/\<uuid\>/wait
         * /1.0/operations/\<uuid\>/websocket
     * /1.0/profiles
       * /1.0/profiles/\<name\>

# API details
## /
### GET
 * Description: List of supported APIs
 * Authentication: guest
 * Operation: sync
 * Return: list of supported API endpoint URLs (by default ['/1.0'])

## /1.0/
### GET
 * Description: Server configuration and environment information
 * Authentication: guest, untrusted or trusted
 * Operation: sync
 * Return: Dict representing server state

Return value (if trusted):

    {
        'auth': "trusted",                              # Authentication state, one of "guest", "untrusted" or "trusted"
        'public': true,                                 # Whether the server should be treated as a public (read-only) remote by the client
        'api_compat': 0,                                # Used to determine API functionality
        'config': {"trust_password": true},             # Host configuration
        'environment': {                                # Various information about the host (OS, kernel, ...)
                        'addresses': ["1.2.3.4:8443", "[1234::1234]:8443"],
                        'architectures': [1, 2],
                        'driver': "lxc",
                        'driver_version': "1.0.6",
                        'kernel': "Linux",
                        'kernel_architecture': "x86_64",
                        'kernel_version': "3.16",
                        'storage': "btrfs",
                        'storage_version': "3.19",
                        'server': "lxd",
                        'server_pid': 10224,
                        'server_version': "0.8.1"}
    }

Return value (if guest or untrusted):

    {
        'auth': "guest",                        # Authentication state, one of "guest", "untrusted" or "trusted"
        'api_compat': 0,                        # Used to determine API functionality
    }

### PUT
 * Description: Updates the server configuration or other properties
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        'config': {"trust_password": "my-new-password"}
    }

## /1.0/containers
### GET
 * Description: List of containers
 * Authentication: trusted
 * Operation: sync
 * Return: list of URLs for containers this server publishes

### POST
 * Description: Create a new container
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Input (container based on a local image with the "ubuntu/devel" alias):

    {
        'name': "my-new-container",                                         # 64 chars max, ASCII, no slash, no colon and no comma
        'architecture': 2,
        'profiles': ["default"],                                            # List of profiles
        'ephemeral': true,                                                  # Whether to destroy the container on shutdown
        'config': {'limits.cpu': "2"},                                      # Config override.
        'source': {'type': "image",                                         # Can be: "image", "migration", "copy" or "none"
                   'alias': "ubuntu/devel"},                                # Name of the alias
    }

Input (container based on a local image identified by its fingerprint):

    {
        'name': "my-new-container",                                         # 64 chars max, ASCII, no slash, no colon and no comma
        'architecture': 2,
        'profiles': ["default"],                                            # List of profiles
        'ephemeral': true,                                                  # Whether to destroy the container on shutdown
        'config': {'limits.cpu': "2"},                                      # Config override.
        'source': {'type': "image",                                         # Can be: "image", "migration", "copy" or "none"
                   'fingerprint': "SHA-256"},                               # Fingerprint
    }

Input (container based on most recent match based on image properties):

    {
        'name': "my-new-container",                                         # 64 chars max, ASCII, no slash, no colon and no comma
        'architecture': 2,
        'profiles': ["default"],                                            # List of profiles
        'ephemeral': true,                                                  # Whether to destroy the container on shutdown
        'config': {'limits.cpu': "2"},                                      # Config override.
        'source': {'type': "image",                                         # Can be: "image", "migration", "copy" or "none"
                   'properties': {                                          # Properties
                        'os': "ubuntu",
                        'release': "14.04",
                        'architecture': "x86_64"
                    }},
    }

Input (container without a pre-populated rootfs, useful when attaching to an existing one):

    {
        'name': "my-new-container",                                         # 64 chars max, ASCII, no slash, no colon and no comma
        'architecture': 2,
        'profiles': ["default"],                                            # List of profiles
        'ephemeral': true,                                                  # Whether to destroy the container on shutdown
        'config': {'limits.cpu': "2"},                                      # Config override.
        'source': {'type': "none"},                                         # Can be: "image", "migration", "copy" or "none"
    }

Input (using a public remote image):

    {
        'name': "my-new-container",                                         # 64 chars max, ASCII, no slash, no colon and no comma
        'architecture': 2,
        'profiles': ["default"],                                            # List of profiles
        'ephemeral': true,                                                  # Whether to destroy the container on shutdown
        'config': {'limits.cpu': "2"},                                      # Config override.
        'source': {'type': "image",                                         # Can be: "image", "migration", "copy" or "none"
                   'mode': "pull",                                          # One of "local" (default), "pull" or "receive"
                   'server': "https://10.0.2.3:8443",                       # Remote server (pull mode only)
                   'alias': "ubuntu/devel"},                                # Name of the alias
    }


Input (using a private remote image after having obtained a secret for that image):

    {
        'name': "my-new-container",                                         # 64 chars max, ASCII, no slash, no colon and no comma
        'architecture': 2,
        'profiles': ["default"],                                            # List of profiles
        'ephemeral': true,                                                  # Whether to destroy the container on shutdown
        'config': {'limits.cpu': "2"},                                      # Config override.
        'source': {'type': "image",                                         # Can be: "image", "migration", "copy" or "none"
                   'mode': "pull",                                          # One of "local" (default), "pull" or "receive"
                   'server': "https://10.0.2.3:8443",                       # Remote server (pull mode only)
                   'secret': "my-secret-string",                            # Secret to use to retrieve the image (pull mode only)
                   'alias': "ubuntu/devel"},                                # Name of the alias
    }

Input (using a remote container, sent over the migration websocket):

    {
        'name': "my-new-container",                                                     # 64 chars max, ASCII, no slash, no colon and no comma
        'architecture': 2,
        'profiles': ["default"],                                                        # List of profiles
        'ephemeral': true,                                                              # Whether to destroy the container on shutdown
        'config': {'limits.cpu': "2"},                                                  # Config override.
        'source': {'type': "migration",                                                 # Can be: "image", "migration", "copy" or "none"
                   'mode': "pull",                                                      # One of "pull" or "receive"
                   'operation': "https://10.0.2.3:8443/1.0/operations/<UUID>",          # Full URL to the remote operation (pull mode only)
                   'base-image': "<some hash>"                                          # Optional, the base image the container was created from
                   'secrets': {'control': "my-secret-string",                           # Secrets to use when talking to the migration source
                               'criu':    "my-other-secret",
                               'fs':      "my third secret"},
    }

Input (using a local container):

    {
        'name': "my-new-container",                                                     # 64 chars max, ASCII, no slash, no colon and no comma
        'architecture': 2,
        'profiles': ["default"],                                                        # List of profiles
        'ephemeral': true,                                                              # Whether to destroy the container on shutdown
        'config': {'limits.cpu': "2"},                                                  # Config override.
        'source': {'type': "copy",                                                      # Can be: "image", "migration", "copy" or "none"
                   'source': "my-old-container"}                                        # Name of the source container
    }


## /1.0/containers/\<name\>
### GET
 * Description: Container information
 * Authentication: trusted
 * Operation: sync
 * Return: dict of the container configuration and current state.

Output:

    {
        'name': "my-container",
        'profiles': ["default"],
        'architecture': 2,
        'config': {"limits.cpu": "3"},
        'expanded_config': {"limits.cpu": "3"}  # the result of expanding profiles and adding the container's local config
        'devices': {
            'rootfs': {
                'type': "disk",
                'path': "/",
                'source': "UUID=8f7fdf5e-dc60-4524-b9fe-634f82ac2fb6"
            }
        },
        'expanded_devices': {  # the result of expanding profiles and adding the container's local devices
            'rootfs': {
                'type': "disk",
                'path': "/",
                'source': "UUID=8f7fdf5e-dc60-4524-b9fe-634f82ac2fb6"}
            },
            "eth0": {
                "type": "nic"
                "parent": "lxcbr0",
                "hwaddr": "00:16:3e:f4:e7:1c",
                "name": "eth0",
                "nictype": "bridged",
            }
        },
        'status': {
                    'status': "Running",
                    'status_code': 103,
                    'ips': [{'interface': "eth0",
                             'protocol': "INET6",
                             'address': "2001:470:b368:1020:1::2",
                             'host_veth': "vethGMDIY9"},
                            {'interface': "eth0",
                             'protocol': "INET",
                             'address': "172.16.15.30",
                             'host_veth': "vethGMDIY9"}]},
    }


### PUT
 * Description: update container configuration or restore snapshot
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Input (update container configuration):

Takes the same structure as that returned by GET but doesn't allow name
changes (see POST below) or changes to the status sub-dict (since that's
read-only).

Input (restore snapshot):

    {
        'restore': "snapshot-name"
    }

### POST
 * Description: used to rename/migrate the container
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Renaming to an existing name must return the 409 (Conflict) HTTP code.

Input (simple rename):

    {
        'name': "new-name"
    }

Input (migration across lxd instances):
    {
        "migration": true,
    }

The migration does not actually start until someone (i.e. another lxd instance)
connects to all the websockets and begins negotiation with the source.

Output in metadata section (for migration):

    {
        "control": "secret1",
        "criu": "secret2",
        "fs": "secret3",
    }

These are the secrets that should be passed to the create call.

### DELETE
 * Description: remove the container
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Input (none at present):

    {
    }

HTTP code for this should be 202 (Accepted).

## /1.0/containers/\<name\>/state
### GET
 * Description: current state
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing current state

    {
        'status': "Running",
        'status_code': 103
    }

### PUT
 * Description: change the container state
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Input:

    {
        'action': "stop",       # State change action (stop, start, restart, freeze or unfreeze)
        'timeout': 30,          # A timeout after which the state change is considered as failed
        'force': true           # Force the state change (currently only valid for stop and restart where it means killing the container)
    }

## /1.0/containers/\<name\>/files
### GET (?path=/path/inside/the/container)
 * Description: download a file from the container
 * Authentication: trusted
 * Operation: sync
 * Return: Raw file or standard error

The following headers will be set (on top of standard size and mimetype headers):
 * X-LXD-uid: 0
 * X-LXD-gid: 0
 * X-LXD-mode: 0700

This is designed to be easily usable from the command line or even a web
browser. This is only supported for currently running containers.

### POST (?path=/path/inside/the/container)
 * Description: upload a file to the container
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:
 * Standard http file upload

The following headers may be set by the client:
 * X-LXD-uid: 0
 * X-LXD-gid: 0
 * X-LXD-mode: 0700

This is designed to be easily usable from the command line or even a web
browser. This is only supported for currently running containers.

## /1.0/containers/\<name\>/snapshots
### GET
 * Description: List of snapshots
 * Authentication: trusted
 * Operation: sync
 * Return: list of URLs for snapshots for this container

### POST
 * Description: create a new snapshot
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Input:

    {
        'name': "my-snapshot",          # Name of the snapshot
        'stateful': true                # Whether to include state too
    }

## /1.0/containers/\<name\>/snapshots/\<name\>
### GET
 * Description: Snapshot information
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing the snapshot

Return:

    {
        'name': "my-snapshot",
        'stateful': true
    }

### POST
 * Description: used to rename/migrate the snapshot
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Input:

    {
        'name': "new-name"
    }

Input (copy snapshot across lxd instances):

    {
        "migration": true,
    }

Renaming to an existing name must return the 409 (Conflict) HTTP code.

### DELETE
 * Description: remove the snapshot
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Input (none at present):

    {
    }

HTTP code for this should be 202 (Accepted).

## /1.0/containers/\<name\>/exec
### POST
 * Description: run a remote command
 * Authentication: trusted
 * Operation: async
 * Return: background operation + optional websocket information or standard error

Input (run bash):

    {
        'command': ["/bin/bash"],       # Command and arguments
        'environment': {},              # Optional extra environment variables to set
        'wait-for-websocket': false,    # Whether to wait for a connection before starting the process
        'interactive': true             # Whether to allocate a pts device instead of PIPEs
    }

`wait-for-websocket` indicates whether the operation should block and wait for
a websocket connection to start (so that users can pass stdin and read
stdout), or simply run to completion with /dev/null as stdin and stdout.

When the exec command finishes, its exit status is available from the
operation's metadata:

    {
        'return': 0
    }

If interactive is set to true, a single websocket is returned and is mapped to a
pts device for stdin, stdout and stderr of the execed process.

If interactive is set to false (default), three pipes will be setup, one
for each of stdin, stdout and stderr.

Depending on the state of the interactive flag, one or three different
websocket/secret pairs will be returned, which are valid for connecting to this
operations /websocket endpoint.

Response metadata (interactive=true); the output of the pty is mirrored over
this websocket:

    {
        "-1": "secret"
    }

Response metadata (interactive=false); each of the process' fds are hooked up
to these (i.e. you can only write to 0, and read from 1 and 2):

    {
        "0": "secret0",
        "1": "secret1",
        "2": "secret2",
    }

## /1.0/containers/\<name\>/logs
### GET
* Description: Returns a list of the log files available for this container.
  Note that this works on containers that have been deleted (or were never
  created) to enable people to get logs for failed creations.
* Authentication: trusted
* Operation: Sync
* Return: a list of the available log files

Return:

    [
        'lxc.log',
        'migration_dump_2015-03-31T14:30:59Z.log'
    ]

## /1.0/containers/\<name\>/logs/\<logfile\>
### GET
* Description: returns the contents of a particular log file.
* Authentication: trusted
* Operation: N/A
* Return: the contents of the log file

### DELETE
* Description: delete a particular log file.
* Authentication: trusted
* Operation: Sync
* Return: empty response or standard error

## /1.0/events
This URL isn't a real REST API endpoint, instead doing a GET query on it
will upgrade the connection to a websocket on which notifications will
be sent.

### GET (?type=operation,logging)
 * Description: websocket upgrade
 * Authentication: trusted
 * Operation: sync
 * Return: none (never ending flow of events)

Supported arguments are:
 * type: comma separated list of notifications to subscribe to (defaults to all)

The notification types are:
 * operation
 * logging

This never returns. Each notification is sent as a separate JSON dict:

    {
        'timestamp': "2015-06-09T19:07:24.379615253-06:00",                # Current timestamp
        'type': "operation",                                               # Notification type
        'metadata': {}                                                     # Extra resource or type specific metadata
    }

    {
        'timestamp': "2015-06-09T19:07:24.379615253-06:00",
        'type': "logging",
        'metadata' {'message': "Service started"}
    }


## /1.0/images
### GET (?key=value&key1=value1...)
 * Description: list of images (public or private)
 * Authentication: guest or trusted
 * Operation: sync
 * Return: list of URLs for images this server publishes

Filtering can be done by specifying a list of key and values in the
query URL.

### POST
 * Description: create and publish a new image
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Input (one of):
 * Standard http file upload
 * Source image dictionary (transfers a remote image)
 * Source container dictionary (makes an image out of a local container)
 * Remote image URL dictionary (downloads a remote image)

In the http file upload case, The following headers may be set by the client:
 * X-LXD-fingerprint: SHA-256 (if set, uploaded file must match)
 * X-LXD-filename: FILENAME (used for export)
 * X-LXD-public: true/false (defaults to false)
 * X-LXD-properties: URL-encoded key value pairs without duplicate keys (optional properties)

In the source image case, the following dict must be passed:

    {
        "public": true,                         # true or false
        "source": {
            "type": "image",
            "mode": "pull",                     # One of "pull" or "receive"
            "server": "https://10.0.2.3:8443",  # Remote server (pull mode only)
            "secret": "my-secret-string",       # Secret (pull mode only, private images only)
            "fingerprint": "SHA256",            # Fingerprint of the image (must be set if alias isn't)
            "alias": "ubuntu/devel",            # Name of the alias (must be set if fingerprint isn't)
        }
    }

In the source container case, the following dict must be passed:

    {
        "public":   true,         # true or false
        "filename": filename,     # Used for export
        "source": {
            "type": "container",  # One of "container" or "snapshot"
            "name": "abc"
        },
        "properties": {           # Image properties
            "os": "Ubuntu",
        }
    }

In the remote image URL case, the following dict must be passed:

    {
        "public": true,                                    # true or false
        "source": {
            "type": "url",
            "url": "https://www.some-server.com/image"     # URL for the image
        }
    }


After the input is received by LXD, a background operation is started
which will add the image to the store and possibly do some backend
filesystem-specific optimizations.

## /1.0/images/\<fingerprint\>
### GET (optional secret=SECRET)
 * Description: Image description and metadata
 * Authentication: guest or trusted
 * Operation: sync
 * Return: dict representing an image properties

Output:

    {
        'aliases': ['alias1', ...],
        'architecture': 2,
        'fingerprint': "a3625aeada09576673620d1d048eed7ac101c4c2409f527a1f77e237fa280e32",
        'filename': "busybox-amd64.tar.xz",
        'properties': {
            'key': 'value'
        },
        'public': true,
        'size': 11031704,
        'created_at': "2015-06-09T19:07:24.379615253-06:00",
        'expires_at': "2015-06-19T19:07:24.379615253-06:00",
        'uploaded_at': "2015-06-09T19:07:24.379615253-06:00"
    }

### DELETE
 * Description: Remove an image
 * Authentication: trusted
 * Operation: async
 * Return: background operaton or standard error

Input (none at present):

    {
    }

HTTP code for this should be 202 (Accepted).

### PUT
 * Description: Updates the image properties
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        'properties': {
            'architecture': "x86_64",
            'description': "Some description"
        },
        'public': false
    }

## /1.0/images/\<fingerprint\>/export
### GET (optional secret=SECRET)
 * Description: Download the image tarball
 * Authentication: guest or trusted
 * Operation: sync
 * Return: Raw file or standard error

The secret string is required when an untrusted LXD is spawning a new
container from a private image stored on a different LXD.

Rather than require a trust relationship between the two LXDs, the
client will POST to /1.0/images/\<fingerprint\>/export to get a secret
token which it'll then pass to the target LXD. That target LXD will then
GET the image as a guest, passing the secret token.


## /1.0/images/\<fingerprint\>/secret
### POST
 * Description: Generate a random token and tell LXD to expect it be used by a guest
 * Authentication: guest or trusted
 * Operation: async
 * Return: background operation or standard error

Input:

    {
    }

Output:

Standard backround operation with "secret" set to the generated secret
string in metadata.

The operation should be DELETEd once the secret is no longer in use.

## /1.0/images/aliases
### GET
 * Description: list of aliases (public or private based on image visibility)
 * Authentication: guest or trusted
 * Operation: sync
 * Return: list of URLs for aliases this server knows about

### POST
 * Description: create a new alias
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        'description': "The alias description",
        'target': "SHA-256",
        'name': "alias-name"
    }

## /1.0/images/aliases/\<name\>
### GET
 * Description: Alias description and target
 * Authentication: guest or trusted
 * Operation: sync
 * Return: dict representing an alias description and target

Output:
    {
        'description': "The alias description",
        'target': "SHA-256"
    }

### PUT
 * Description: Updates the alias target or description
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        'description': "New description",
        'target': "SHA-256"
    }

### POST
 * Description: rename an alias
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        'name': "new-name"
    }

Renaming to an existing name must return the 409 (Conflict) HTTP code.

### DELETE
 * Description: Remove an alias
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input (none at present):

    {
    }

## /1.0/networks
### GET
 * Description: list of networks
 * Authentication: trusted
 * Operation: sync
 * Return: list of URLs for networks that are current defined on the host

    [
        "/1.0/networks/eth0",
        "/1.0/networks/lxcbr0"
    ]

## /1.0/networks/\<name\>
### GET
 * Description: information about a network
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing a network

    {
        'name': "lxcbr0",
        'type': "bridge",
        'members': ["/1.0/containers/blah"]
    }

## /1.0/operations
### GET
 * Description: list of operations
 * Authentication: trusted
 * Operation: sync
 * Return: list of URLs for operations that are currently going on/queued

    [
        "/1.0/operations/c0fc0d0d-a997-462b-842b-f8bd0df82507",
        "/1.0/operations/092a8755-fd90-4ce4-bf91-9f87d03fd5bc"
    ]

## /1.0/operations/\<uuid\>
### GET
 * Description: background operation
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing a background operation

Return:

    {
        'created_at': "2015-06-09T19:07:24.379615253-06:00", # Creation timestamp
        'updated_at': "2015-06-09T19:07:24.379615253-06:00", # Last update timestamp
        'status': "Running",
        'status_code': 103,
        'metadata': {},                                      # Extra information about the operation (action, target, ...)
        'may_cancel': true                                   # Whether it's possible to cancel the operation
    }

### DELETE
 * Description: cancel an operation. Calling this will change the state to "cancelling" rather than actually removing the entry.
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input (none at present):

    {
    }

HTTP code for this should be 202 (Accepted).

## /1.0/operations/\<uuid\>/wait
### GET (?status\_code=200&timeout=30)
 * Description: Wait for an operation to finish
 * Authentication: trusted
 * Operation: sync
 * Return: dict of the operation after its reach its final state

Input (wait indefinitely for a final state): no argument

Input (similar by times out after 30s): ?timeout=30

## /1.0/operations/\<uuid\>/websocket
### GET (?secret=...)
 * Description: This connection is upgraded into a websocket connection
   speaking the protocol defined by the operation type. For example, in the
   case of an exec operation, the websocket is the bidirectional pipe for
   stdin/stdout/stderr to flow to and from the process inside the container.
   In the case of migration, it will be the primary interface over which the
   migration information is communicated. The secret here is the one that was
   provided when the operation was created. Guests are allowed to connect
   provided they have the right secret.
 * Authentication: guest or trusted
 * Operation: sync
 * Return: websocket stream or standard error

## /1.0/profiles
### GET
 * Description: List of configuration profiles
 * Authentication: trusted
 * Operation: sync
 * Return: list of URLs to defined profiles

### POST
 * Description: define a new profile
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        'name': "my-profile'name",
        'config': {"resources.memory": "2GB"},
                   "network.0.bridge": "lxcbr0"}
    }

## /1.0/profiles/\<name\>
### GET
 * Description: profile configuration
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing the profile content

Output:

    {
        'name': "my-profile'name",
        'config': {"resources.memory": "2GB"},
                   "network.0.bridge": "lxcbr0"}
    }

### PUT
 * Description: update the profile
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

Same dict as used for initial creation and coming from GET. The name
property can't be changed (see POST for that).


### POST
 * Description: rename a profile
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input (rename a profile):

    {
        'name': "new-name"
    }


HTTP return value must be 204 (No content) and Location must point to
the renamed resource.

Renaming to an existing name must return the 409 (Conflict) HTTP code.


### DELETE
 * Description: remove a profile
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input (none at present):

    {
    }

HTTP code for this should be 202 (Accepted).

## /1.0/certificates
### GET
 * Description: list of trusted certificates
 * Authentication: trusted
 * Operation: sync
 * Return: list of URLs for trusted certificates

### POST
 * Description: add a new trusted certificate
 * Authentication: trusted or untrusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        'type': "client",                       # Certificate type (keyring), currently only client
        'certificate': "BASE64",                # If provided, a valid x509 certificate. If not, the client certificate of the connection will be used
        'name': "foo"                           # An optional name for the certificate. If nothing is provided, the host in the TLS header for the request is used.
        'password': "server-trust-password"     # The trust password for that server (only required if untrusted)
    }

## /1.0/certificates/\<fingerprint\>
### GET
 * Description: trusted certificate information
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing a trusted certificate

Output:

    {
        'type': "client",
        'certificate': "PEM certificate"
        'fingerprint': "SHA256 Hash of the raw certificate"
    }

### DELETE
 * Description: Remove a trusted certificate
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input (none at present):

    {
    }

HTTP code for this should be 202 (Accepted).

# Async operations
Any operation which may take more than a second to be done must be done
in the background, returning a background operation ID to the client.

The client will then be able to either poll for a status update or wait
for a notification using the long-poll API.

# Notifications
A long-poll API is available for notifications, different notification
types exist to limit the traffic going to the client.

It's recommend that the client always subscribes to the operations
notification type before triggering remote operations so that it doesn't
have to then poll for their status.
