# API extensions

The changes below were introduced to the LXD API after the 1.0 API was finalized.

They are all backward compatible and can be detected by client tools by
looking at the api\_extensions field in GET /1.0/.


## storage\_zfs\_remove\_snapshots
A storage.zfs\_remove\_snapshots daemon configuration key was introduced.

It's a boolean that defaults to false and that when set to true instructs LXD
to remove any needed snapshot when attempting to restore another.

This is needed as ZFS will only let you restore the latest snapshot.

## container\_host\_shutdown\_timeout
A boot.host\_shutdown\_timeout container configuration key was introduced.

It's an integer which indicates how long LXD should wait for the container
to stop before killing it.

Its value is only used on clean LXD daemon shutdown. It defaults to 30s.

## container\_syscall\_filtering
A number of new syscalls related container configuration keys were introduced.

 * security.syscalls.blacklist\_default
 * security.syscalls.blacklist\_compat
 * security.syscalls.blacklist
 * security.syscalls.whitelist

See configuration.md for how to use them.

## auth\_pki
This indicates support for PKI authentication mode.

In this mode, the client and server both must use certificates issued by the same PKI.

See lxd-ssl-authentication.md for details.

## container\_last\_used\_at
A last\_used\_at field was added to the /1.0/containers/\<name\> GET endpoint.

It is a timestamp of the last time the container was started.

If a container has been created but not started yet, last\_used\_at field
will be 1970-01-01T00:00:00Z

## etag
Add support for the ETag header on all relevant endpoints.

This adds the following HTTP header on answers to GET:
 - ETag (SHA-256 of user modifiable content)

And adds support for the following HTTP header on PUT requests:
 - If-Match (ETag value retrieved through previous GET)

This makes it possible to GET a LXD object, modify it and PUT it without
risking to hit a race condition where LXD or another client modified the
object in the meantime.

## patch
Add support for the HTTP PATCH method.

PATCH allows for partial update of an object in place of PUT.

## https\_allowed\_credentials
To use LXD API with all Web Browsers (via SPAs) you must send credentials
(certificate) with each XHR (in order for this to happen, you should set
["withCredentials=true"](https://developer.mozilla.org/en-US/docs/Web/API/XMLHttpRequest/withCredentials)
flag to each XHR Request).

Some browsers like Firefox and Safari can't accept server response without
`Access-Control-Allow-Credentials: true` header. To ensure that the server will
return a response with that header, set `core.https_allowed_credentials=true`.

## image\_compression\_algorithm
This adds support for a compression\_algorithm property when creating an image (POST to /1.0/images).

Setting this property overrides the server default value (images.compression\_algorithm).

## directory\_manipulation

This allows for creating and listing directories via the LXD API, and exports
the file type via the X-LXD-type header, which can be either "file" or
"directory" right now.
