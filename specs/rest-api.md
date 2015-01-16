# Introduction
All the communications between lxd and its clients happen using a
RESTful API over http which is then encapsulated over either SSL for
remote operations or a unix socket for local operations.

Not all of the REST interface requires authentication:

 * PUT to /1.0/trust is allowed for everyone with a client certificate
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
        'resources': {
            'containers': ["/1.0/containers/my-container"]      # List of affected resources
        },
        'metadata': {                                           # Metadata relevant to the operation
            'websocket_secret': 'theparadiserocks'              # The secret string used to connect to a websocket.
                                                                # This is optional, depending on whether or not
                                                                # the operation has a websocket you can connect to.
        }
    }

The body is mostly provided as a user friendly way of seeing what's
going on without having to pull the target operation, all information in
the body can also be retrieved from the background operation URL.

### Error
There are various situations in which something may immediately go
wrong, in those cases, the following return value is used:

    {
        'type': "error",
        'status': "Failure",
        'status_code': 400,
        'metadata': {}                      # More details about the error
    }

HTTP code must be one of of 400, 401, 403, 404, 409 or 500.

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
100   | OK
101   | Started
102   | Stopped
103   | Running
200   | Success
400   | Failure


# Safety for concurrent updates
The API uses the HTTP ETAG to prevent potential problems when a resource
changes on the server between the time it was accessed by the client and
the time it is sent back for update.

All GET queries come with an Etag HTTP header which is a short hash of
the content that is relevant for an update. Any information which is
read-only, shouldn't be included in the hash.

On update (PUT), the same Etag field can be set by the client in its
request. If it's set, the server will then compute the current Etag for
the resource and compare the two. The update will then only be done if
the two match. If they don't, an error will be returned instead.

For consistency in lxc's use of hashes, the Etag hash should be a SHA-256.

# API structure
 * /
   * /1.0
     * /1.0/containers
       * /1.0/containers/\<name\>
         * /1.0/containers/\<name\>/exec
         * /1.0/containers/\<name\>/files
         * /1.0/containers/\<name\>/snapshots
         * /1.0/containers/\<name\>/snapshots/\<name\>
         * /1.0/containers/\<name\>/state
     * /1.0/finger
     * /1.0/images
       * /1.0/images/\<name\>
     * /1.0/longpoll
     * /1.0/networks
       * /1.0/networks/\<name\>
     * /1.0/operations
       * /1.0/operations/\<id\>
         * /1.0/operations/\<id\>/wait
         * /1.0/operations/\<id\>/websocket
     * /1.0/profiles
       * /1.0/profiles/\<name\>
     * /1.0/trust
       * /1.0/trust/\<fingerprint\>

# API details
## /
### GET
 * Authentication: guest
 * Operation: sync
 * Return: list of supported API endpoint URLs (by default ['/1.0'])
 * Description: List of supported APIs

## /1.0/
### GET
 * Authentication: trusted
 * Operation: sync
 * Return: Dict representing server state
 * Description: Server configuration and environment information

Return value:

    {
        'config': [{'key': "trust-password",            # Host configuration
                    'value': True}],                    # In the case of passwords, their value is returned as True if set
        'environment': {'kernel_version': "3.16",       # Various information about the host (OS, kernel, ...)
                        'lxc_version': "1.0.6",
                        'driver': "lxc",
                        'backing_fs': "ext4"}
    }

### PUT
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error
 * Description: Updates the server configuration or other properties

Input:

    {
        'config': [{'key': "trust-password",
                    'value': "my-new-password"}]
    }

## /1.0/containers
### GET
 * Authentication: trusted
 * Operation: sync
 * Return: list of URLs for containers this server publishes
 * Description: List of containers

### POST
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error
 * Description: Create a new container

Input (container based on remote image):

    {
        'name': "my-new-container",                                         # 64 chars max, ASCII, no slash, no colon and no comma
        'profiles': ["default"],                                            # List of profiles
        'ephemeral': True,                                                  # Whether to destroy the container on shutdown
        'config': [{'key': 'lxc.aa_profile',                                # Config override. List of dicts to respect ordering and allow flexibility.
                    'value': 'lxc-container-default-with-nesting'},
                   {'key': 'lxc.mount.auto',
                    'value': 'cgroup'}],
        'source': {'type': "remote",                                        # Can be: local (source is a local image, container or snapshot), remote (requires a provided remote config) or proxy (requires a provided ssl socket info)
                   'url': 'https+lxc-images://images.linuxcontainers.org",  # URL for the remote
                   'name': "lxc-images/ubuntu/trusty/amd64",                # Name of the image or container on the remote
                   'metadata': {'gpg_key': "GPG KEY BASE64"}},              # Metadata to setup the remote
    }

Input (clone of a local snapshot):

    {
        'name': "my-new-container",
        'profiles': ["default"],
        'source': {'type': "local",
                   'name': "a/b"},                                          # Use snapshot "b" of container "a" as the source
        'userdata': "BASE64 of userdata"                                    # Userdata exposed over /dev/lxd and used by cloud-init or equivalent tools
    }


## /1.0/containers/\<name\>
### GET
 * Authentication: trusted
 * Operation: sync
 * Return: dict of the container configuration and current state
 * Description: Container information

Input:

    {
        'name': "my-container",
        'profiles': ["default"],
        'config': [{'key': "resources.memory",
                    'value': "50%"}],
        'userdata': "SOME BASE64 BLOB",
        'status': {
                    'status': "Running",
                    'status_code': 103,
                    'ips': [{'interface': "eth0",
                             'protocol': "INET6",
                             'address': "2001:470:b368:1020:1::2"},
                            {'interface': "eth0",
                             'protocol': "INET",
                             'address': "172.16.15.30"}]}
    }


### PUT
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error
 * Description: update container configuration

Input:

Takes the same structure as that returned by GET but doesn't allow name
changes (see POST below) or changes to the status sub-dict (since that's
read-only).

### POST
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error
 * Description: used to rename/migrate the container

Input (simple rename):

    {
        'name': "new-name"
    }


Renaming to an existing name must return the 409 (Conflict) HTTP code.

TODO: Cross host rename/migration.


### DELETE
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error
 * Description: remove the container

Input (none at present):

    {
    }

## /1.0/containers/\<name\>/state
### GET
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing current state
 * Description: current state

    {
        'status': "Running",
        'status_code': 103
    }

### PUT
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error
 * Description: change the container state

Input:

    {
        'action': "stop",       # State change action (stop, start, restart, freeze or unfreeze)
        'timeout': 30,          # A timeout after which the state change is considered as failed
        'force': True           # Force the state change (currently only valid for stop and restart where it means killing the container)
    }

## /1.0/containers/\<name\>/files
### GET (?path=/path/inside/the/container)
 * Authentication: trusted
 * Operation: sync
 * Return: Raw file or standard error
 * Description: download a file from the container

The following headers will be set (on top of standard size and mimetype headers):
 * X-LXD-uid: 0
 * X-LXD-gid: 0
 * X-LXD-mode: 0700

This is designed to be easily usable from the command line or even a web browser.

### PUT
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value  or standard error
 * Description: upload a file to the container

Input:
 * Standard http file upload

The following headers may be set by the client:
 * X-LXD-uid: 0
 * X-LXD-gid: 0
 * X-LXD-mode: 0700

This is designed to be easily usable from the command line or even a web browser.

## /1.0/containers/\<name\>/snapshots
### GET
 * Authentication: trusted
 * Operation: sync
 * Return: list of URLs for snapshots for this container
 * Description: List of snapshots

### POST
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error
 * Description: create a new snapshot

Input:

    {
        'name': "my-snapshot",          # Name of the snapshot
        'stateful': True                # Whether to include state too
    }

## /1.0/containers/\<name\>/snapshots/\<name\>
### GET
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing the snapshot
 * Description: Snapshot information

Return:

    {
        'name': "my-snapshot",
        'stateful': True
    }

### POST
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error
 * Description: used to rename the snapshot

Input:

    {
        'name': "new-name"
    }

Renaming to an existing name must return the 409 (Conflict) HTTP code.

### DELETE
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error
 * Description: remove the snapshot

Input (none at present):

    {
    }

## /1.0/containers/\<name\>/exec
### POST
 * Authentication: trusted
 * Operation: async
 * Return: background operation + websocket information or standard error
 * Description: run a remote command

Input (run bash):

    {
        'command': ["/bin/bash"]
    }

## /1.0/finger
### GET
 * Authentication: guest, untrusted or trusted
 * Operation: sync
 * Return: dict of basic API and auth information
 * Description: returns what's needed for an initial handshake

Return:

    {
        'auth': "guest",                        # Authentication state, one of "guest", "untrusted" or "trusted"
        'api_compat': 0,                        # Used to determine API functionality
    }

Additional information about the server can then be pulled from /1.0 once authenticated.

## /1.0/images
### GET
 * Authentication: guest or trusted
 * Operation: sync
 * Return: list of URLs for images this server publishes
 * Description: list of images (public or private)

### PUT
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error
 * Description: create and publish a new image

Input:

TODO: examples

## /1.0/images/\<name\>
### GET
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing an image description and metadata
 * Description: Image description and metadata

TODO: examples

### DELETE
 * Authentication: trusted
 * Operation: async
 * Return: background operaton or standard error
 * Description: Remove an image

Input (none at present):

    {
    }

### PUT
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error
 * Description: Updates the image metadata

Input:

TODO: examples

### POST
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error
 * Description: rename or move an image

Input (rename an image):

    {
        'name': "new-name"
    }

Renaming to an existing name must return the 409 (Conflict) HTTP code.

TODO: move to remote host

## /1.0/networks
### GET
 * Authentication: trusted
 * Operation: sync
 * Description: list of networks
 * Return: list of URLs for networks that are current defined on the host

    [
        "/1.0/networks/eth0",
        "/1.0/networks/lxcbr0"
    ]

## /1.0/networks/\<name\>
### GET
 * Authentication: trusted
 * Operation: sync
 * Description: information about a network
 * Return: dict representing a network

    {
        'name': "lxcbr0",
        'type': "bridge",
        'members': ["/1.0/containers/blah"]
    }

## /1.0/operations
### GET
 * Authentication: trusted
 * Operation: sync
 * Description: list of operations
 * Return: list of URLs for operations that are currently going on/queued

    [
        "/1.0/operations/c0fc0d0d-a997-462b-842b-f8bd0df82507",
        "/1.0/operations/092a8755-fd90-4ce4-bf91-9f87d03fd5bc"
    ]

## /1.0/operations/\<uuid\>
### GET
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing a background operation
 * Description: background operation

Return:

    {
        'created_at': 1415639996,                   # Creation timestamp
        'updated_at': 1415639996,                   # Last update timestamp
        'status': "Running",
        'status_code': 103,
        'resources': {
            'containers': ['/1.0/containers/1']     # List of affected resources
        },
        'metadata': {},                             # Extra information about the operation (action, target, ...)
        'may_cancel': True                          # Whether it's possible to cancel the operation
    }

### DELETE
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error
 * Description: cancel an operation. Calling this will change the state to "cancelling" rather than actually removing the entry.

Input (none at present):

    {
    }

## /1.0/operations/\<uuid\>/wait
### GET (?status\_code=200&timeout=30)
 * Authentication: trusted
 * Operation: sync
 * Return: dict of the operation once its state changes to the request state
 * Description: Wait for an operation to finish

Input (wait for any event): no argument

Input (wait for the operation to succeed or timeout): ?status\_code=200&timeout=30

## /1.0/operations/\<uuid\>/websocket
### GET (?secret=...)
 * Authentication: trusted
 * Operation: sync
 * Return: websocket stream or standard error
 * Description: This connection is upgraded into a websocket connection
   speaking the protocol defined by the operation type. For example, in the
   case of an exec operation, the websocket is the bidirectional pipe for
   stdin/stdout/stderr to flow to and from the process inside the container.
   In the case of migration, it will be the primary interface over which the
   migration information is communicated. The secret here is the one that was
   provided when the operation was created.

## /1.0/profiles
### GET
 * Authentication: trusted
 * Operation: sync
 * Return: list of URLs to defined profiles
 * Description: List of configuration profiles

### PUT
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error
 * Description: define a new profile

Input:

    {
        'name': "my-profile'name",
        'config': [{'key': "resources.memory",
                    'value': "2GB"},
                   {'key': "network.0.bridge",
                    'value': "lxcbr0"}]
    }

## /1.0/profiles/\<name\>
### GET
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing the profile content
 * Description: profile configuration

Output:

    {
        'name': "my-profile'name",
        'config': [{'key': "resources.memory",
                    'value': "2GB"},
                   {'key': "network.0.bridge",
                    'value': "lxcbr0"}]
    }

### PUT
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error
 * Description: update the profile

Input:

Same dict as used for initial creation and coming from GET. The name
property can't be changed (see POST for that).


### POST
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error
 * Description: rename or move a profile

Input (rename a profile):

    {
        'name': "new-name"
    }


HTTP return value must be 204 (No content) and Location must point to
the renamed resource.

Renaming to an existing name must return the 409 (Conflict) HTTP code.

TODO: move profile to another host


### DELETE
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error
 * Description: remove a profile

Input (none at present):

    {
    }


## /1.0/trust
### GET
 * Authentication: trusted
 * Operation: sync
 * Return: list of URLs for trusted certificates
 * Description: list of trusted certificates

### POST
 * Authentication: trusted or untrusted
 * Operation: sync
 * Return: standard return value or standard error
 * Description: add a new trusted certificate

Input:

    {
        'type': "client",                       # Certificate type (keyring), currently only client
        'certificate': "BASE64",                # If provided, a valid x509 certificate. If not, the client certificate of the connection will be used
        'name': "foo"                           # An optional name for the certificate. If nothing is provided, the host in the TLS header for the request is used.
        'password': "server-trust-password"     # The trust password for that server (only required if untrusted)
    }

## /1.0/trust/\<fingerprint\>
### GET
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing a trusted certificate
 * Description: trusted certificate information

Output:

    {
        'type': "client",
        'certificate': "BASE64"
    }

### DELETE
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error
 * Description: Remove a trusted certificate

Input (none at present):

    {
    }

## /1.0/longpoll
This URL isn't a standard REST object, instead it's a longpoll service
which will send notifications to the client when a background operation
changes state.

The same mechanism may also be used for some live logging output.

### POST
 * Authentication: trusted
 * Operation: sync
 * Return: none (never ending flow of events)
 * Description: long-poll API

POST is the only supported method for this endpoint.

The following JSON dict must be passed as argument:

    {
        'type': [], # List of notification types (initially "operations" or "logging").
    }

This never returns. Each notification is sent as a separate JSON dict:

    {
        'timestamp': 1415639996,                # Current timestamp
        'type': "operations",                   # Notification type
        'resource': "/1.0/operations/<id>",     # Resource URL
        'metadata': {}                          # Extra resource or type specific metadata
    }

    {
        'timestamp': 1415639996,
        'type': "logging",
        'resource': "/1.0",
        'metadata' {'message': "Service started"}
    }


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
