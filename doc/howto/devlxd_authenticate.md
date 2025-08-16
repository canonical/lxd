(devlxd-authenticate)=
# How to authenticate to the DevLXD API

The DevLXD API is available inside guest instances to allow limited interaction with the host (see {ref}`dev-lxd`).

This API is available unauthenticated, since LXD determines the source instance and returns information only for that workload.
However, advanced use cases may require the caller to be authenticated.

To authenticate over the DevLXD API, first create a `DevLXD token bearer` identity:

`````{tabs}
```{group-tab} CLI
    lxc auth identity create devlxd/<name> [[--group <group> ]]
```
```{group-tab} API
    lxc query --request POST /1.0/auth/identities/bearer --data '{
      "name": "<name>",
      "type": "DevLXD token bearer"
      "groups": [
        "<group>"
      ]
    }'
```
`````

Next, issue a token for the identity:

`````{tabs}
```{group-tab} CLI
    lxc auth identity token issue devlxd/<name> [--expiry <expiry> ]
```
```{group-tab} API
    lxc query --request POST /1.0/auth/identities/bearer/<name>/token --data '{
      "expiry": "<expiry>"
    }'
```
`````

The returned token can be used to authenticate with LXD over the DevLXD socket.
It must be set as a bearer token in the `Authorization` header.

You can verify trust by checking the `auth` field in the response of `GET /1.0`:

    $ lxc exec c1 --env TOKEN=${token} -- bash
    root@c1# curl -H "Authorization: Bearer ${TOKEN}" -s --unix-socket /dev/lxd/sock http://custom.socket/1.0
    {"state":"Started","api_version":"1.0","instance_type":"container","location":"my-host","auth":"trusted"}
