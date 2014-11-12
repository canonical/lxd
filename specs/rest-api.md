# Introduction
All the communications between lxd and its clients happen using a
RESTful API over http which is then encapsulated over either SSL for
remote operations or a unix socket for local operations.

Not all of the REST interface requires authentication:

 * PUT to /1.0/trust is allowed for everyone with a client certificate
 * GET to /1.0/images/\* is allowed for everyone but only returns public images for unauthenticated users

Unauthenticated endpoints are clearly identified as such below.

# Return values
There are three standard return types:
 * Standard return value
 * Background operation
 * Error

### Standard return value
For a standard synchronous operation, the following dict is returned:

    {
        'type': "sync",
        'result': "success",                    # Result string ("success", "failure")
        'metadata': {}                          # Extra resource/action specific metadata
    }

HTTP code must be 200.

### Background operation
For an async operation, the following dict is returned:

    {
        'type': "async",
        'operation': "/1.0/operations/<id>",    # URL to the background operation
    }

HTTP code must be 200.

### Error
There are various situations in which something may immediately go
wrong, in those cases, the following return value is used:

    {
        'type': "error",
        'code': 500,        # HTTP error code
        'metadata': {}      # More details about the error
    }

HTTP code must be one of of 400, 401, 403, 404 or 500.

# Basic structure
## /1.0/containers
### GET
Return a list of URLs for images this server publishes.

### PUT
Create a new container. (WIP)

## /1.0/containers/\<name\>
### GET
Return detailed information about the container. (WIP)

### PUT
Update container metadata. (WIP)

### POST
Used to rename/move the container. (WIP)

### DELETE
Remove the container.

Background operation.

## /1.0/images
### GET
Return a list of URLs for images this server publishes.

### PUT
Publish a new image based on an existing container or snapshot. (WIP)

## /1.0/images/\<name\>
### GET
Return detailed information about the image. (WIP)

### DELETE
Remove the image.

Background operation.

### PUT
Update image metadata. (WIP)

### POST
Used to rename/move the image. (WIP)

## /1.0/operations
### GET
Return a list of URLs to every active operations.

## /1.0/operations/\<id\>
### GET
Get the detail about the operation.

    {
        'created_at': 1415639996,               # Creation timestamp
        'updated_at': 1415639996,               # Last update timestamp
        'status': "running",                    # Status string ("pending", "running", "done", "cancelling", "cancelled")
        'resullt': "",                          # Result string ("success", "failure")
        'resource_url': '/1.0/containers/1',    # Affected resource
        'metadata': {},                         # Extra information about the operation (action, target, ...)
        'may_cancel': True                      # Whether it's possible to cancel the operation
    }

### DELETE
Cancel an operation. Calling this will change the state to "cancelling"
rather than actually removing the entry.

Uses a standard return value.

## /1.0/trust
### GET
Return a list of URLs for trusted certificates.

### PUT (unauthenticated)
Add a new trusted certificate.

    {
        'type': "client",                       # Certificate type (keyring), currently only client
        'certificate': "BASE64",                # If provided, a valid x509 certificate. If not, the client certificate of the connection will be used
        'password': "server-trust-password"     # The trust password for that server
    }

This is a sync operation.

## /1.0/trust/\<fingerprint\>
### GET
Return detailed information about a certificate.

    {
        'type': "client",
        'certificate': "BASE64"
    }

### DELETE

Remove a trusted certificate.

This is a sync operation.

## /1.0/longpoll
This URL isn't a standard REST object, instead it's a longpoll service
which will send notifications to the client when a background operation
changes state.

The same mechanism may also be used for some live logging output.

### POST
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
