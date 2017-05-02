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
result in addition to api\_extensions which can be used by the client
to check if a given feature is supported by the server.

# Return values
There are three standard return types:
 * Standard return value
 * Background operation
 * Error

### Standard return value
For a standard synchronous operation, the following dict is returned:

    {
        "type": "sync",
        "status": "Success",
        "status_code": 200,
        "metadata": {}                          # Extra resource/action specific metadata
    }

HTTP code must be 200.

### Background operation
When a request results in a background operation, the HTTP code is set to 202 (Accepted)
and the Location HTTP header is set to the operation URL.

The body is a dict with the following structure:

    {
        "type": "async",
        "status": "OK",
        "status_code": 100,
        "operation": "/1.0/containers/<id>",                    # URL to the background operation
        "metadata": {}                                          # Operation metadata (see below)
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
        "type": "error",
        "error": "Failure",
        "error_code": 400,
        "metadata": {}                      # More details about the error
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

# Recursion
To optimize queries of large lists, recursion is implemented for collections.
A "recursion" argument can be passed to a GET query against a collection.

The default value is 0 which means that collection member URLs are
returned. Setting it to 1 will have those URLs be replaced by the object
they point to (typically a dict).

Recursion is implemented by simply replacing any pointer to an job (URL)
by the object itself.

# Async operations
Any operation which may take more than a second to be done must be done
in the background, returning a background operation ID to the client.

The client will then be able to either poll for a status update or wait
for a notification using the long-poll API.

# Notifications
A websocket based API is available for notifications, different notification
types exist to limit the traffic going to the client.

It's recommended that the client always subscribes to the operations
notification type before triggering remote operations so that it doesn't
have to then poll for their status.

# PUT vs PATCH
The LXD API supports both PUT and PATCH to modify existing objects.

PUT replaces the entire object with a new definition, it's typically
called after the current object state was retrieved through GET.

To avoid race conditions, the Etag header should be read from the GET
response and sent as If-Match for the PUT request. This will cause LXD
to fail the request if the object was modified between GET and PUT.

PATCH can be used to modify a single field inside an object by only
specifying the property that you want to change. To unset a key, setting
it to empty will usually do the trick, but there are cases where PATCH
won't work and PUT needs to be used instead.

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
 * Return: list of supported API endpoint URLs

Return value:

    [
        "/1.0"
    ]

## /1.0/
### GET
 * Description: Server configuration and environment information
 * Authentication: guest, untrusted or trusted
 * Operation: sync
 * Return: Dict representing server state

Return value (if trusted):

    {
        "api_extensions": [],                           # List of API extensions added after the API was marked stable
        "api_status": "stable",                         # API implementation status (one of, development, stable or deprecated)
        "api_version": "1.0",                           # The API version as a string
        "auth": "trusted",                              # Authentication state, one of "guest", "untrusted" or "trusted"
        "config": {                                     # Host configuration
            "core.trust_password": true,
            "core.https_address": "[::]:8443"
        },
        "environment": {                                # Various information about the host (OS, kernel, ...)
            "addresses": [
                "1.2.3.4:8443",
                "[1234::1234]:8443"
            ],
            "architectures": [
                "x86_64",
                "i686"
            ],
            "certificate": "PEM certificate",
            "driver": "lxc",
            "driver_version": "1.0.6",
            "kernel": "Linux",
            "kernel_architecture": "x86_64",
            "kernel_version": "3.16",
            "server": "lxd",
            "server_pid": 10224,
            "server_version": "0.8.1"}
            "storage": "btrfs",
            "storage_version": "3.19",
        },
        "public": false,                                # Whether the server should be treated as a public (read-only) remote by the client
    }

Return value (if guest or untrusted):

    {
        "api_extensions": [],                   # List of API extensions added after the API was marked stable
        "api_status": "stable",                 # API implementation status (one of, development, stable or deprecated)
        "api_version": "1.0",                   # The API version as a string
        "auth": "guest",                        # Authentication state, one of "guest", "untrusted" or "trusted"
        "public": false,                        # Whether the server should be treated as a public (read-only) remote by the client
    }

### PUT (ETag supported)
 * Description: Replaces the server configuration or other properties
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input (replaces any existing config with the provided one):

    {
        "config": {
            "core.trust_password": "my-new-password",
            "core.https_address": "1.2.3.4:8443"
        }
    }

### PATCH (ETag supported)
 * Description: Updates the server configuration or other properties
 * Introduced: with API extension "patch"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input (updates only the listed keys, rest remains intact):

    {
        "config": {
            "core.trust_password": "my-new-password"
        }
    }

## /1.0/certificates
### GET
 * Description: list of trusted certificates
 * Authentication: trusted
 * Operation: sync
 * Return: list of URLs for trusted certificates

Return:

    [
        "/1.0/certificates/3ee64be3c3c7d617a7470e14f2d847081ad467c8c26e1caad841c8f67f7c7b09"
    ]

### POST
 * Description: add a new trusted certificate
 * Authentication: trusted or untrusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "type": "client",                       # Certificate type (keyring), currently only client
        "certificate": "PEM certificate",       # If provided, a valid x509 certificate. If not, the client certificate of the connection will be used
        "name": "foo",                          # An optional name for the certificate. If nothing is provided, the host in the TLS header for the request is used.
        "password": "server-trust-password"     # The trust password for that server (only required if untrusted)
    }

## /1.0/certificates/\<fingerprint\>
### GET
 * Description: trusted certificate information
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing a trusted certificate

Output:

    {
        "type": "client",
        "certificate": "PEM certificate",
        "name": "foo",
        "fingerprint": "SHA256 Hash of the raw certificate"
    }

### PUT (ETag supported)
 * Description: Replaces the certificate properties
 * Introduced: with API extension "certificate\_update"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "type": "client",
        "name": "bar"
    }

### PATCH (ETag supported)
 * Description: Updates the certificate properties
 * Introduced: with API extension "certificate\_update"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "name": "baz"
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

## /1.0/containers
### GET
 * Description: List of containers
 * Authentication: trusted
 * Operation: sync
 * Return: list of URLs for containers this server publishes

Return value:

    [
        "/1.0/containers/blah",
        "/1.0/containers/blah1"
    ]

### POST
 * Description: Create a new container
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Input (container based on a local image with the "ubuntu/devel" alias):

    {
        "name": "my-new-container",                                         # 64 chars max, ASCII, no slash, no colon and no comma
        "architecture": "x86_64",
        "profiles": ["default"],                                            # List of profiles
        "ephemeral": true,                                                  # Whether to destroy the container on shutdown
        "config": {"limits.cpu": "2"},                                      # Config override.
        "devices": {                                                        # optional list of devices the container should have
            "kvm": {
                "path": "/dev/kvm",
                "type": "unix-char"
            },
        },
        "source": {"type": "image",                                         # Can be: "image", "migration", "copy" or "none"
                   "alias": "ubuntu/devel"},                                # Name of the alias
    }

Input (container based on a local image identified by its fingerprint):

    {
        "name": "my-new-container",                                         # 64 chars max, ASCII, no slash, no colon and no comma
        "architecture": "x86_64",
        "profiles": ["default"],                                            # List of profiles
        "ephemeral": true,                                                  # Whether to destroy the container on shutdown
        "config": {"limits.cpu": "2"},                                      # Config override.
        "devices": {                                                        # optional list of devices the container should have
            "kvm": {
                "path": "/dev/kvm",
                "type": "unix-char"
            },
        },
        "source": {"type": "image",                                         # Can be: "image", "migration", "copy" or "none"
                   "fingerprint": "SHA-256"},                               # Fingerprint
    }

Input (container based on most recent match based on image properties):

    {
        "name": "my-new-container",                                         # 64 chars max, ASCII, no slash, no colon and no comma
        "architecture": "x86_64",
        "profiles": ["default"],                                            # List of profiles
        "ephemeral": true,                                                  # Whether to destroy the container on shutdown
        "config": {"limits.cpu": "2"},                                      # Config override.
        "devices": {                                                        # optional list of devices the container should have
            "kvm": {
                "path": "/dev/kvm",
                "type": "unix-char"
            },
        },
        "source": {"type": "image",                                         # Can be: "image", "migration", "copy" or "none"
                   "properties": {                                          # Properties
                        "os": "ubuntu",
                        "release": "14.04",
                        "architecture": "x86_64"
                    }},
    }

Input (container without a pre-populated rootfs, useful when attaching to an existing one):

    {
        "name": "my-new-container",                                         # 64 chars max, ASCII, no slash, no colon and no comma
        "architecture": "x86_64",
        "profiles": ["default"],                                            # List of profiles
        "ephemeral": true,                                                  # Whether to destroy the container on shutdown
        "config": {"limits.cpu": "2"},                                      # Config override.
        "devices": {                                                        # optional list of devices the container should have
            "kvm": {
                "path": "/dev/kvm",
                "type": "unix-char"
            },
        },
        "source": {"type": "none"},                                         # Can be: "image", "migration", "copy" or "none"
    }

Input (using a public remote image):

    {
        "name": "my-new-container",                                         # 64 chars max, ASCII, no slash, no colon and no comma
        "architecture": "x86_64",
        "profiles": ["default"],                                            # List of profiles
        "ephemeral": true,                                                  # Whether to destroy the container on shutdown
        "config": {"limits.cpu": "2"},                                      # Config override.
        "devices": {                                                        # optional list of devices the container should have
            "kvm": {
                "path": "/dev/kvm",
                "type": "unix-char"
            },
        },
        "source": {"type": "image",                                         # Can be: "image", "migration", "copy" or "none"
                   "mode": "pull",                                          # One of "local" (default) or "pull"
                   "server": "https://10.0.2.3:8443",                       # Remote server (pull mode only)
                   "protocol": "lxd",                                       # Protocol (one of lxd or simplestreams, defaults to lxd)
                   "certificate": "PEM certificate",                        # Optional PEM certificate. If not mentioned, system CA is used.
                   "alias": "ubuntu/devel"},                                # Name of the alias
    }

Input (using a private remote image after having obtained a secret for that image):

    {
        "name": "my-new-container",                                         # 64 chars max, ASCII, no slash, no colon and no comma
        "architecture": "x86_64",
        "profiles": ["default"],                                            # List of profiles
        "ephemeral": true,                                                  # Whether to destroy the container on shutdown
        "config": {"limits.cpu": "2"},                                      # Config override.
        "devices": {                                                        # optional list of devices the container should have
            "kvm": {
                "path": "/dev/kvm",
                "type": "unix-char"
            },
        },
        "source": {"type": "image",                                         # Can be: "image", "migration", "copy" or "none"
                   "mode": "pull",                                          # One of "local" (default) or "pull"
                   "server": "https://10.0.2.3:8443",                       # Remote server (pull mode only)
                   "secret": "my-secret-string",                            # Secret to use to retrieve the image (pull mode only)
                   "certificate": "PEM certificate",                        # Optional PEM certificate. If not mentioned, system CA is used.
                   "alias": "ubuntu/devel"},                                # Name of the alias
    }

Input (using a remote container, sent over the migration websocket):

    {
        "name": "my-new-container",                                                     # 64 chars max, ASCII, no slash, no colon and no comma
        "architecture": "x86_64",
        "profiles": ["default"],                                                        # List of profiles
        "ephemeral": true,                                                              # Whether to destroy the container on shutdown
        "config": {"limits.cpu": "2"},                                                  # Config override.
        "devices": {                                                                    # optional list of devices the container should have
            "kvm": {
                "path": "/dev/kvm",
                "type": "unix-char"
            },
        },
        "source": {"type": "migration",                                                 # Can be: "image", "migration", "copy" or "none"
                   "mode": "pull",                                                      # "pull" and "push" is supported for now
                   "operation": "https://10.0.2.3:8443/1.0/operations/<UUID>",          # Full URL to the remote operation (pull mode only)
                   "certificate": "PEM certificate",                                    # Optional PEM certificate. If not mentioned, system CA is used.
                   "base-image": "<fingerprint>",                                       # Optional, the base image the container was created from
                   "container_only": "true",                                            # Whether to migrate only the container without snapshots. Can be "true" or "false".
                   "secrets": {"control": "my-secret-string",                           # Secrets to use when talking to the migration source
                               "criu":    "my-other-secret",
                               "fs":      "my third secret"},
    }

Input (using a local container):

    {
        "name": "my-new-container",                                                     # 64 chars max, ASCII, no slash, no colon and no comma
        "profiles": ["default"],                                                        # List of profiles
        "ephemeral": true,                                                              # Whether to destroy the container on shutdown
        "config": {"limits.cpu": "2"},                                                  # Config override.
        "devices": {                                                                    # optional list of devices the container should have
            "kvm": {
                "path": "/dev/kvm",
                "type": "unix-char"
            },
        },
        "source": {"type": "copy",                                                      # Can be: "image", "migration", "copy" or "none"
                   "container_only": "true",                                            # Whether to copy only the container without snapshots. Can be "true" or "false".
                   "source": "my-old-container"}                                        # Name of the source container
    }

Input (using a remote container, in push mode sent over the migration websocket via client proxying):

    {
        "name": "my-new-container",                                                     # 64 chars max, ASCII, no slash, no colon and no comma
        "architecture": "x86_64",
        "profiles": ["default"],                                                        # List of profiles
        "ephemeral": true,                                                              # Whether to destroy the container on shutdown
        "config": {"limits.cpu": "2"},                                                  # Config override.
        "devices": {                                                                    # optional list of devices the container should have
            "kvm": {
                "path": "/dev/kvm",
                "type": "unix-char"
            },
        },
        "source": {"type": "migration",                                                 # Can be: "image", "migration", "copy" or "none"
                   "mode": "push",                                                      # "pull" and "push" are supported
                   "base-image": "<fingerprint>",                                       # Optional, the base image the container was created from
                   "live": true                                                         # Whether migration is performed live
                   "container_only": "true",                                            # Whether to migrate only the container without snapshots. Can be "true" or "false".
    }

## /1.0/containers/\<name\>
### GET
 * Description: Container information
 * Authentication: trusted
 * Operation: sync
 * Return: dict of the container configuration and current state.

Output:

    {
        "architecture": "x86_64",
        "config": {
            "limits.cpu": "3",
            "volatile.base_image": "97d97a3d1d053840ca19c86cdd0596cf1be060c5157d31407f2a4f9f350c78cc",
            "volatile.eth0.hwaddr": "00:16:3e:1c:94:38"
        },
        "created_at": "2016-02-16T01:05:05Z",
        "devices": {
            "rootfs": {
                "path": "/",
                "type": "disk"
            }
        },
        "ephemeral": false,
        "expanded_config": {    # the result of expanding profiles and adding the container's local config
            "limits.cpu": "3",
            "volatile.base_image": "97d97a3d1d053840ca19c86cdd0596cf1be060c5157d31407f2a4f9f350c78cc",
            "volatile.eth0.hwaddr": "00:16:3e:1c:94:38"
        },
        "expanded_devices": {   # the result of expanding profiles and adding the container's local devices
            "eth0": {
                "name": "eth0",
                "nictype": "bridged",
                "parent": "lxdbr0",
                "type": "nic"
            },
            "root": {
                "path": "/",
                "type": "disk"
            }
        },
        "last_used_at": "2016-02-16T01:05:05Z",
        "name": "my-container",
        "profiles": [
            "default"
        ],
        "stateful": false,      # If true, indicates that the container has some stored state that can be restored on startup
        "status": "Running",
        "status_code": 103
    }

### PUT (ETag supported)
 * Description: replaces container configuration or restore snapshot
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Input (update container configuration):

    {
        "architecture": "x86_64",
        "config": {
            "limits.cpu": "4",
            "volatile.base_image": "97d97a3d1d053840ca19c86cdd0596cf1be060c5157d31407f2a4f9f350c78cc",
            "volatile.eth0.hwaddr": "00:16:3e:1c:94:38"
        },
        "devices": {
            "rootfs": {
                "path": "/",
                "type": "disk"
            }
        },
        "ephemeral": true,
        "profiles": [
            "default"
        ]
    }

Takes the same structure as that returned by GET but doesn't allow name
changes (see POST below) or changes to the status sub-dict (since that's
read-only).

Input (restore snapshot):

    {
        "restore": "snapshot-name"
    }

### PATCH (ETag supported)
 * Description: update container configuration
 * Introduced: with API extension "patch"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "config": {
            "limits.cpu": "4"
        },
        "devices": {
            "rootfs": {
                "size": "5GB"
            }
        },
        "ephemeral": true
    }

### POST
 * Description: used to rename/migrate the container
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Renaming to an existing name must return the 409 (Conflict) HTTP code.

Input (simple rename):

    {
        "name": "new-name"
    }

Input (migration across lxd instances):
    {
        "migration": true
    }

The migration does not actually start until someone (i.e. another lxd instance)
connects to all the websockets and begins negotiation with the source.

Output in metadata section (for migration):

    {
        "control": "secret1",       # Migration control socket
        "criu": "secret2",          # State transfer socket (only if live migrating)
        "fs": "secret3"             # Filesystem transfer socket
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

## /1.0/containers/\<name\>/exec
### POST
 * Description: run a remote command
 * Authentication: trusted
 * Operation: async
 * Return: background operation + optional websocket information or standard error

Input (run bash):

    {
        "command": ["/bin/bash"],       # Command and arguments
        "environment": {},              # Optional extra environment variables to set
        "wait-for-websocket": false,    # Whether to wait for a connection before starting the process
        "record-output": false,         # Whether to store stdout and stderr (only valid with wait-for-websocket=false) (requires API extension container_exec_recording)
        "interactive": true,            # Whether to allocate a pts device instead of PIPEs
        "width": 80,                    # Initial width of the terminal (optional)
        "height": 25,                   # Initial height of the terminal (optional)
    }

`wait-for-websocket` indicates whether the operation should block and wait for
a websocket connection to start (so that users can pass stdin and read
stdout), or start immediately.

If starting immediately, /dev/null will be used for stdin, stdout and
stderr. That's unless record-output is set to true, in which case,
stdout and stderr will be redirected to a log file.

If interactive is set to true, a single websocket is returned and is mapped to a
pts device for stdin, stdout and stderr of the execed process.

If interactive is set to false (default), three pipes will be setup, one
for each of stdin, stdout and stderr.

Depending on the state of the interactive flag, one or three different
websocket/secret pairs will be returned, which are valid for connecting to this
operations /websocket endpoint.

Return (with wait-for-websocket=true and interactive=false):

    {
        "fds": {
            "0": "f5b6c760c0aa37a6430dd2a00c456430282d89f6e1661a077a926ed1bf3d1c21",
            "1": "464dcf9f8fdce29d0d6478284523a9f26f4a31ae365d94cd38bac41558b797cf",
            "2": "25b70415b686360e3b03131e33d6d94ee85a7f19b0f8d141d6dca5a1fc7b00eb",
            "control": "20c479d9532ab6d6c3060f6cdca07c1f177647c9d96f0c143ab61874160bd8a5"
        }
    }

Return (with wait-for-websocket=true and interactive=true):

    {
        "fds": {
            "0": "f5b6c760c0aa37a6430dd2a00c456430282d89f6e1661a077a926ed1bf3d1c21",
            "control": "20c479d9532ab6d6c3060f6cdca07c1f177647c9d96f0c143ab61874160bd8a5"
        }
    }

When the exec command finishes, its exit status is available from the
operation's metadata:

    {
        "return": 0
    }

## /1.0/containers/\<name\>/files
### GET (?path=/path/inside/the/container)
 * Description: download a file or directory listing from the container
 * Authentication: trusted
 * Operation: sync
 * Return: if the type of the file is a directory, the return is a sync
   response with a list of the directory contents as metadata, otherwise it is
   the raw contents of the file.

The following headers will be set (on top of standard size and mimetype headers):
 * X-LXD-uid: 0
 * X-LXD-gid: 0
 * X-LXD-mode: 0700
 * X-LXD-type: one of "directory" or "file"

This is designed to be easily usable from the command line or even a web
browser.

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
 * X-LXD-type: one of "directory" or "file"
 * X-LXD-write: overwrite (or append, introduced with API extension "file\_append")

This is designed to be easily usable from the command line or even a web
browser.

### DELETE (?path=/path/inside/the/container)
 * Description: delete a file in the container
 * Introduced: with API extension "file\_delete"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input (none at present):

    {
    }

## /1.0/containers/\<name\>/snapshots
### GET
 * Description: List of snapshots
 * Authentication: trusted
 * Operation: sync
 * Return: list of URLs for snapshots for this container

Return value:

    [
        "/1.0/containers/blah/snapshots/snap0"
    ]

### POST
 * Description: create a new snapshot
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Input:

    {
        "name": "my-snapshot",          # Name of the snapshot
        "stateful": true                # Whether to include state too
    }

## /1.0/containers/\<name\>/snapshots/\<name\>
### GET
 * Description: Snapshot information
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing the snapshot

Return:

    {
        "architecture": "x86_64",
        "config": {
            "security.nesting": "true",
            "volatile.base_image": "a49d26ce5808075f5175bf31f5cb90561f5023dcd408da8ac5e834096d46b2d8",
            "volatile.eth0.hwaddr": "00:16:3e:ec:65:a8",
            "volatile.last_state.idmap": "[{\"Isuid\":true,\"Isgid\":false,\"Hostid\":100000,\"Nsid\":0,\"Maprange\":65536},{\"Isuid\":false,\"Isgid\":true,\"Hostid\":100000,\"Nsid\":0,\"Maprange\":65536}]",
        },
        "created_at": "2016-03-08T23:55:08Z",
        "devices": {
            "eth0": {
                "name": "eth0",
                "nictype": "bridged",
                "parent": "lxdbr0",
                "type": "nic"
            },
            "root": {
                "path": "/",
                "type": "disk"
            },
        },
        "ephemeral": false,
        "expanded_config": {
            "security.nesting": "true",
            "volatile.base_image": "a49d26ce5808075f5175bf31f5cb90561f5023dcd408da8ac5e834096d46b2d8",
            "volatile.eth0.hwaddr": "00:16:3e:ec:65:a8",
            "volatile.last_state.idmap": "[{\"Isuid\":true,\"Isgid\":false,\"Hostid\":100000,\"Nsid\":0,\"Maprange\":65536},{\"Isuid\":false,\"Isgid\":true,\"Hostid\":100000,\"Nsid\":0,\"Maprange\":65536}]",
        },
        "expanded_devices": {
            "eth0": {
                "name": "eth0",
                "nictype": "bridged",
                "parent": "lxdbr0",
                "type": "nic"
            },
            "root": {
                "path": "/",
                "type": "disk"
            },
        },
        "name": "zerotier/blah",
        "profiles": [
            "default"
        ],
        "stateful": false
    }

### POST
 * Description: used to rename/migrate the snapshot
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Input (rename the snapshot):

    {
        "name": "new-name"
    }

Input (setup the migration source):

    {
        "migration": true,
    }

Return (with migration=true):

    {
        "control": "secret1",       # Migration control socket
        "fs": "secret3"             # Filesystem transfer socket
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

## /1.0/containers/\<name\>/state
### GET
 * Description: current state
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing current state

Output:

    {
        "type": "sync",
        "status": "Success",
        "status_code": 200,
        "metadata": {
            "status": "Running",
            "status_code": 103,
            "cpu": {
                "usage": 4986019722
            },
            "disk": {
                "root": {
                    "usage": 422330368
                }
            },
            "memory": {
                "usage": 51126272,
                "usage_peak": 70246400,
                "swap_usage": 0,
                "swap_usage_peak": 0
            },
            "network": {
                "eth0": {
                    "addresses": [
                        {
                            "family": "inet",
                            "address": "10.0.3.27",
                            "netmask": "24",
                            "scope": "global"
                        },
                        {
                            "family": "inet6",
                            "address": "fe80::216:3eff:feec:65a8",
                            "netmask": "64",
                            "scope": "link"
                        }
                    ],
                    "counters": {
                        "bytes_received": 33942,
                        "bytes_sent": 30810,
                        "packets_received": 402,
                        "packets_sent": 178
                    },
                    "hwaddr": "00:16:3e:ec:65:a8",
                    "host_name": "vethBWTSU5",
                    "mtu": 1500,
                    "state": "up",
                    "type": "broadcast"
                },
                "lo": {
                    "addresses": [
                        {
                            "family": "inet",
                            "address": "127.0.0.1",
                            "netmask": "8",
                            "scope": "local"
                        },
                        {
                            "family": "inet6",
                            "address": "::1",
                            "netmask": "128",
                            "scope": "local"
                        }
                    ],
                    "counters": {
                        "bytes_received": 86816,
                        "bytes_sent": 86816,
                        "packets_received": 1226,
                        "packets_sent": 1226
                    },
                    "hwaddr": "",
                    "host_name": "",
                    "mtu": 65536,
                    "state": "up",
                    "type": "loopback"
                },
                "lxdbr0": {
                    "addresses": [
                        {
                            "family": "inet",
                            "address": "10.0.3.1",
                            "netmask": "24",
                            "scope": "global"
                        },
                        {
                            "family": "inet6",
                            "address": "fe80::68d4:87ff:fe40:7769",
                            "netmask": "64",
                            "scope": "link"
                        }
                    ],
                    "counters": {
                        "bytes_received": 0,
                        "bytes_sent": 570,
                        "packets_received": 0,
                        "packets_sent": 7
                    },
                    "hwaddr": "6a:d4:87:40:77:69",
                    "host_name": "",
                    "mtu": 1500,
                    "state": "up",
                    "type": "broadcast"
               },
               "zt0": {
                    "addresses": [
                        {
                            "family": "inet",
                            "address": "29.17.181.59",
                            "netmask": "7",
                            "scope": "global"
                        },
                        {
                            "family": "inet6",
                            "address": "fd80:56c2:e21c:0:199:9379:e711:b3e1",
                            "netmask": "88",
                            "scope": "global"
                        },
                        {
                            "family": "inet6",
                            "address": "fe80::79:e7ff:fe0d:5123",
                            "netmask": "64",
                            "scope": "link"
                        }
                    ],
                    "counters": {
                        "bytes_received": 0,
                        "bytes_sent": 806,
                        "packets_received": 0,
                        "packets_sent": 9
                    },
                    "hwaddr": "02:79:e7:0d:51:23",
                    "host_name": "",
                    "mtu": 2800,
                    "state": "up",
                    "type": "broadcast"
                }
            },
            "pid": 13663,
            "processes": 32
        }
    }

### PUT
 * Description: change the container state
 * Authentication: trusted
 * Operation: async
 * Return: background operation or standard error

Input:

    {
        "action": "stop",       # State change action (stop, start, restart, freeze or unfreeze)
        "timeout": 30,          # A timeout after which the state change is considered as failed
        "force": true,          # Force the state change (currently only valid for stop and restart where it means killing the container)
        "stateful": true        # Whether to store or restore runtime state before stopping or startiong (only valid for stop and start, defaults to false)
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
        "/1.0/containers/blah/logs/forkstart.log",
        "/1.0/containers/blah/logs/lxc.conf",
        "/1.0/containers/blah/logs/lxc.log"
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
 * operation (notification about creation, updates and termination of all background operations)
 * logging (every log entry from the server)

This never returns. Each notification is sent as a separate JSON dict:

    {
        "timestamp": "2015-06-09T19:07:24.379615253-06:00",                # Current timestamp
        "type": "operation",                                               # Notification type
        "metadata": {}                                                     # Extra resource or type specific metadata
    }

    {
        "timestamp": "2016-02-17T11:44:28.572721913-05:00",
        "type": "logging",
        "metadata": {
            "context": {
                "ip": "@",
                "method": "GET"
                "url": "/1.0/containers/xen/snapshots",
            },
            "level": "info",
            "message": "handling"
        }
    }

## /1.0/images
### GET
 * Description: list of images (public or private)
 * Authentication: guest or trusted
 * Operation: sync
 * Return: list of URLs for images this server publishes

Return:

    [
        "/1.0/images/54c8caac1f61901ed86c68f24af5f5d3672bdc62c71d04f06df3a59e95684473",
        "/1.0/images/97d97a3d1d053840ca19c86cdd0596cf1be060c5157d31407f2a4f9f350c78cc",
        "/1.0/images/a49d26ce5808075f5175bf31f5cb90561f5023dcd408da8ac5e834096d46b2d8",
        "/1.0/images/c9b6e738fae75286d52f497415463a8ecc61bbcb046536f220d797b0e500a41f"
    ]

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

In the source image case, the following dict must be used:

    {
        "filename": filename,                   # Used for export (optional)
        "public": true,                         # Whether the image can be downloaded by untrusted users (defaults to false)
        "auto_update": true,                    # Whether the image should be auto-updated (optional; defaults to false)
        "properties": {                         # Image properties (optional, applied on top of source properties)
            "os": "Ubuntu"
        },
        "aliases": [                            # Set initial aliases ("image_create_aliases" API extension)
            {"name": "my-alias",
             "description: "A description"
        },
        "source": {
            "type": "image",
            "mode": "pull",                     # Only pull is supported for now
            "server": "https://10.0.2.3:8443",  # Remote server (pull mode only)
            "protocol": "lxd",                  # Protocol (one of lxd or simplestreams, defaults to lxd)
            "secret": "my-secret-string",       # Secret (pull mode only, private images only)
            "certificate": "PEM certificate",   # Optional PEM certificate. If not mentioned, system CA is used.
            "fingerprint": "SHA256",            # Fingerprint of the image (must be set if alias isn't)
            "alias": "ubuntu/devel",            # Name of the alias (must be set if fingerprint isn't)
        }
    }

In the source container case, the following dict must be used:

    {
        "compression_algorithm": "xz",  # Override the compression algorithm for the image (optional)
        "filename": filename,           # Used for export (optional)
        "public":   true,               # Whether the image can be downloaded by untrusted users (defaults to false)
        "properties": {                 # Image properties (optional)
            "os": "Ubuntu"
        },
        "aliases": [                    # Set initial aliases ("image_create_aliases" API extension)
            {"name": "my-alias",
             "description: "A description"
        },
        "source": {
            "type": "container",        # One of "container" or "snapshot"
            "name": "abc"
        }
    }

In the remote image URL case, the following dict must be used:

    {
        "filename": filename,                           # Used for export (optional)
        "public":   true,                               # Whether the image can be downloaded by untrusted users  (defaults to false)
        "properties": {                                 # Image properties (optional)
            "os": "Ubuntu"
        },
        "aliases": [                                    # Set initial aliases ("image_create_aliases" API extension)
            {"name": "my-alias",
             "description: "A description"
        },
        "source": {
            "type": "url",
            "url": "https://www.some-server.com/image"  # URL for the image
        }
    }

After the input is received by LXD, a background operation is started
which will add the image to the store and possibly do some backend
filesystem-specific optimizations.

## /1.0/images/\<fingerprint\>
### GET (optional ?secret=SECRET)
 * Description: Image description and metadata
 * Authentication: guest or trusted
 * Operation: sync
 * Return: dict representing an image properties

Output:

    {
        "aliases": [
            {
                "name": "trusty",
                "description": "",
            }
        ],
        "architecture": "x86_64",
        "auto_update": true,
        "cached": false,
        "fingerprint": "54c8caac1f61901ed86c68f24af5f5d3672bdc62c71d04f06df3a59e95684473",
        "filename": "ubuntu-trusty-14.04-amd64-server-20160201.tar.xz",
        "properties": {
            "architecture": "x86_64",
            "description": "Ubuntu 14.04 LTS server (20160201)",
            "os": "ubuntu",
            "release": "trusty"
        },
        "update_source": {
            "server": "https://10.1.2.4:8443",
            "protocol": "lxd",
            "certificate": "PEM certificate",
            "alias": "ubuntu/trusty/amd64"
        },
        "public": false,
        "size": 123792592,
        "created_at": "2016-02-01T21:07:41Z",
        "expires_at": "1970-01-01T00:00:00Z",
        "last_used_at": "1970-01-01T00:00:00Z",
        "uploaded_at": "2016-02-16T00:44:47Z"
    }

### PUT (ETag supported)
 * Description: Replaces the image properties, update information and visibility
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "auto_update": true,
        "properties": {
            "architecture": "x86_64",
            "description": "Ubuntu 14.04 LTS server (20160201)",
            "os": "ubuntu",
            "release": "trusty"
        },
        "public": true,
    }

### PATCH (ETag supported)
 * Description: Updates the image properties, update information and visibility
 * Introduced: with API extension "patch"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "properties": {
            "os": "ubuntu",
            "release": "trusty"
        },
        "public": true,
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

## /1.0/images/\<fingerprint\>/export
### GET (optional ?secret=SECRET)
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

Return:

    {
        "secret": "52e9ec5885562aa24d05d7b4846ebb8b5f1f7bf5cd6e285639b569d9eaf54c9b"
    }

Standard backround operation with "secret" set to the generated secret
string in metadata.

The secret is automatically invalidated 5s after an image URL using it
has been accessed. This allows to both retried the image information and
then hit /export with the same secret.

## /1.0/images/aliases
### GET
 * Description: list of aliases (public or private based on image visibility)
 * Authentication: guest or trusted
 * Operation: sync
 * Return: list of URLs for aliases this server knows about

Return:

    [
        "/1.0/images/aliases/sl6",
        "/1.0/images/aliases/trusty",
        "/1.0/images/aliases/xenial"
    ]

### POST
 * Description: create a new alias
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "description": "The alias description",
        "target": "SHA-256",
        "name": "alias-name"
    }

## /1.0/images/aliases/\<name\>
### GET
 * Description: Alias description and target
 * Authentication: guest or trusted
 * Operation: sync
 * Return: dict representing an alias description and target

Output:

    {
        "name": "test",
        "description": "my description",
        "target": "c9b6e738fae75286d52f497415463a8ecc61bbcb046536f220d797b0e500a41f"
    }

### PUT (ETag supported)
 * Description: Replaces the alias target or description
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "description": "New description",
        "target": "54c8caac1f61901ed86c68f24af5f5d3672bdc62c71d04f06df3a59e95684473"
    }

### PATCH (ETag supported)
 * Description: Updates the alias target or description
 * Introduced: with API extension "patch"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "description": "New description"
    }

### POST
 * Description: rename an alias
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "name": "new-name"
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
        "/1.0/networks/lxdbr0"
    ]

### POST
 * Description: define a new network
 * Introduced: with API extension "network"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "name": "my-network",
        "description": "My network",
        "config": {
            "ipv4.address": "none",
            "ipv6.address": "2001:470:b368:4242::1/64",
            "ipv6.nat": "true"
        }
    }

## /1.0/networks/\<name\>
### GET
 * Description: information about a network
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing a network

    {
        "config": {},
        "name": "lxdbr0",
        "managed": false,
        "type": "bridge",
        "used_by": [
            "/1.0/containers/blah"
        ]
    }

### PUT (ETag supported)
 * Description: replace the network information
 * Introduced: with API extension "network"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "config": {
            "bridge.driver": "openvswitch",
            "ipv4.address": "10.0.3.1/24",
            "ipv6.address": "fd1:6997:4939:495d::1/64"
        }
    }

Same dict as used for initial creation and coming from GET. Only the
config is used, everything else is ignored.

### PATCH (ETag supported)
 * Description: update the network information
 * Introduced: with API extension "network"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "config": {
            "dns.mode": "dynamic"
        }
    }

### POST
 * Description: rename a network
 * Introduced: with API extension "network"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input (rename a network):

    {
        "name": "new-name"
    }

HTTP return value must be 204 (No content) and Location must point to
the renamed resource.

Renaming to an existing name must return the 409 (Conflict) HTTP code.

### DELETE
 * Description: remove a network
 * Introduced: with API extension "network"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input (none at present):

    {
    }

HTTP code for this should be 202 (Accepted).

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
        "id": "b8d84888-1dc2-44fd-b386-7f679e171ba5",
        "class": "token",                                                                       # One of "task" (background task), "websocket" (set of websockets and crendentials) or "token" (temporary credentials)
        "created_at": "2016-02-17T16:59:27.237628195-05:00",                                    # Creation timestamp
        "updated_at": "2016-02-17T16:59:27.237628195-05:00",                                    # Last update timestamp
        "status": "Running",
        "status_code": 103,
        "resources": {                                                                          # List of affected resources
            "images": [
                "/1.0/images/54c8caac1f61901ed86c68f24af5f5d3672bdc62c71d04f06df3a59e95684473"
            ]
        },
        "metadata": {                                                                           # Extra information about the operation (action, target, ...)
            "secret": "c9209bee6df99315be1660dd215acde4aec89b8e5336039712fc11008d918b0d"
        },
        "may_cancel": true,                                                                     # Whether it's possible to cancel the operation (DELETE)
        "err": ""
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
### GET (optional ?timeout=30)
 * Description: Wait for an operation to finish
 * Authentication: trusted
 * Operation: sync
 * Return: dict of the operation after it's reached its final state

Input (wait indefinitely for a final state): no argument

Input (similar but times out after 30s): ?timeout=30

## /1.0/operations/\<uuid\>/websocket
### GET (?secret=SECRET)
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

Return:

    [
        "/1.0/profiles/default"
    ]

### POST
 * Description: define a new profile
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "name": "my-profilename",
        "description": "Some description string",
        "config": {
            "limits.memory": "2GB"
        },
        "devices": {
            "kvm": {
                "type": "unix-char",
                "path": "/dev/kvm"
            }
        }
    }

## /1.0/profiles/\<name\>
### GET
 * Description: profile configuration
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing the profile content

Output:

    {
        "name": "test",
        "description": "Some description string",
        "config": {
            "limits.memory": "2GB"
        },
        "devices": {
            "kvm": {
                "path": "/dev/kvm",
                "type": "unix-char"
            }
        },
        "used_by": [
            "/1.0/containers/blah"
        ]
    }

### PUT (ETag supported)
 * Description: replace the profile information
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "config": {
            "limits.memory": "4GB"
        },
        "description": "Some description string",
        "devices": {
            "kvm": {
                "path": "/dev/kvm",
                "type": "unix-char"
            }
        }
    }

Same dict as used for initial creation and coming from GET. The name
property can't be changed (see POST for that).

### PATCH (ETag supported)
 * Description: update the profile information
 * Introduced: with API extension "patch"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "config": {
            "limits.memory": "4GB"
        },
        "description": "Some description string",
        "devices": {
            "kvm": {
                "path": "/dev/kvm",
                "type": "unix-char"
            }
        }
    }

### POST
 * Description: rename a profile
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input (rename a profile):

    {
        "name": "new-name"
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

## /1.0/storage-pools
### GET
 * Description: list of storage pools
 * Introduced: with API extension "storage"
 * Authentication: trusted
 * Operation: sync
 * Return: list of storage pools that are currently defined on the host

    [
        "/1.0/storage-pools/default",
        "/1.0/storage-pools/pool1"
        "/1.0/storage-pools/pool2"
        "/1.0/storage-pools/pool3"
        "/1.0/storage-pools/pool4"
    ]

### POST
 * Description: create a new storage pool
 * Introduced: with API extension "storage"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "config": {
            "size": "10GB"
        },
        "driver": "zfs",
        "name": "pool1"
    }

## /1.0/storage-pools/<name>
### GET
 * Description: information about a storage pool
 * Introduced: with API extension "storage"
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing a storage pool

    {
        "type": "sync",
        "status": "Success",
        "status_code": 200,
        "operation": "",
        "error_code": 0,
        "error": "",
        "metadata": {
            "name": "default",
            "driver": "zfs",
            "used_by": [
                "/1.0/containers/alp1",
                "/1.0/containers/alp10",
                "/1.0/containers/alp11",
                "/1.0/containers/alp12",
                "/1.0/containers/alp13",
                "/1.0/containers/alp14",
                "/1.0/containers/alp15",
                "/1.0/containers/alp16",
                "/1.0/containers/alp17",
                "/1.0/containers/alp18",
                "/1.0/containers/alp19",
                "/1.0/containers/alp2",
                "/1.0/containers/alp20",
                "/1.0/containers/alp3",
                "/1.0/containers/alp4",
                "/1.0/containers/alp5",
                "/1.0/containers/alp6",
                "/1.0/containers/alp7",
                "/1.0/containers/alp8",
                "/1.0/containers/alp9",
                "/1.0/images/62e850a334bb9d99cac00b2e618e0291e5e7bb7db56c4246ecaf8e46fa0631a6"
            ],
            "config": {
                "size": "61203283968",
                "source": "/home/chb/mnt/l2/disks/default.img",
                "volume.size": "0",
                "zfs.pool_name": "default"
            }
        }
    }

### PUT (ETag supported)
 * Description: replace the storage pool information
 * Introduced: with API extension "storage"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

 Input:

    {
        "config": {
            "size": "15032385536",
            "source": "pool1",
            "volume.block.filesystem": "xfs",
            "volume.block.mount_options": "discard",
            "lvm.thinpool_name": "LXDPool",
            "lvm.vg_name": "pool1",
            "volume.size": "10737418240"
        }
    }

### PATCH
 * Description: update the storage pool configuration
 * Introduced: with API extension "storage"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "config": {
            "volume.block.filesystem": "xfs",
        }
    }

### DELETE
 * Description: delete a storage pool
 * Introduced: with API extension "storage"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input (none at present):

    {
    }

## /1.0/storage-pools/<name>/volumes
### GET
 * Description: list of storage volumes
 * Introduced: with API extension "storage"
 * Authentication: trusted
 * Operation: sync
 * Return: list of storage volumes that currently exist on a given storage pool

    [
        "/1.0/storage-pools/default/volumes/containers/alp1",
        "/1.0/storage-pools/default/volumes/containers/alp10",
        "/1.0/storage-pools/default/volumes/containers/alp11",
        "/1.0/storage-pools/default/volumes/containers/alp12",
        "/1.0/storage-pools/default/volumes/containers/alp13",
        "/1.0/storage-pools/default/volumes/containers/alp14",
        "/1.0/storage-pools/default/volumes/containers/alp15",
        "/1.0/storage-pools/default/volumes/containers/alp16",
        "/1.0/storage-pools/default/volumes/containers/alp17",
        "/1.0/storage-pools/default/volumes/containers/alp18",
        "/1.0/storage-pools/default/volumes/containers/alp19",
        "/1.0/storage-pools/default/volumes/containers/alp2",
        "/1.0/storage-pools/default/volumes/containers/alp20",
        "/1.0/storage-pools/default/volumes/containers/alp3",
        "/1.0/storage-pools/default/volumes/containers/alp4",
        "/1.0/storage-pools/default/volumes/containers/alp5",
        "/1.0/storage-pools/default/volumes/containers/alp6",
        "/1.0/storage-pools/default/volumes/containers/alp7",
        "/1.0/storage-pools/default/volumes/containers/alp8",
        "/1.0/storage-pools/default/volumes/containers/alp9",
        "/1.0/storage-pools/default/volumes/images/62e850a334bb9d99cac00b2e618e0291e5e7bb7db56c4246ecaf8e46fa0631a6"
    ]

## /1.0/storage-pools/<pool>/volumes
### GET
 * Description: list all storage volumes on a storage pool
 * Introduced: with API extension "storage"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

    {
        "type": "sync",
        "status": "Success",
        "status_code": 200,
        "error_code": 0,
        "error": "",
        "metadata": [
            {
                "type": "container",
                "used_by": [],
                "name": "alp1",
                "config": {
                "size": "0"
                }
            },
            {
                "type": "container",
                "used_by": [],
                "name": "alp1/snap0",
                "config": {
                    "size": "0"
                }
            },
            {
                "type": "image",
                "used_by": [],
                "name": "ade3a9bcd7ba27456673611304238c424ced1772f69d7c6b031356831d94e8ee",
                "config": {
                    "size": "0"
                }
            },
            {
                "type": "custom",
                "used_by": [],
                "name": "bla",
                "config": {
                    "size": "0"
                }
            }
        ]
    }


### POST
 * Description: create a new storage volume on a given storage pool
 * Introduced: with API extension "storage"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input:

    {
        "config": {},
        "pool": "pool1",
        "name": "vol1",
        "type": "custom"
    }


## /1.0/storage-pools/<pool>/volumes/<type>/<name>
### GET
 * Description: information about a storage volume of a given type on a storage pool
 * Introduced: with API extension "storage"
 * Authentication: trusted
 * Operation: sync
 * Return: dict representing a storage volume

    {
        "type": "sync",
        "status": "Success",
        "status_code": 200,
        "error_code": 0,
        "error": "",
        "metadata": {
            "type": "custom",
            "used_by": [],
            "name": "vol1",
            "config": {
                "block.filesystem": "ext4",
                "block.mount_options": "discard",
                "size": "10737418240"
            }
        }
    }


### PUT (ETag supported)
 * Description: replace the storage volume information
 * Introduced: with API extension "storage"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

 Input:

    {
        "config": {
            "size": "15032385536",
            "source": "pool1",
            "used_by": "",
            "volume.block.filesystem": "xfs",
            "volume.block.mount_options": "discard",
            "lvm.thinpool_name": "LXDPool",
            "lvm.vg_name": "pool1",
            "volume.size": "10737418240"
        }
    }

### PATCH (ETag supported)
 * Description: update the storage volume information
 * Introduced: with API extension "storage"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

 Input:

    {
        "config": {
            "volume.block.mount_options": "",
        }
    }

### DELETE
 * Description: delete a storage volume of a given type on a given storage pool
 * Introduced: with API extension "storage"
 * Authentication: trusted
 * Operation: sync
 * Return: standard return value or standard error

Input (none at present):

    {
    }
