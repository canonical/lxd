(howto-auth-bearer)=
# How to authenticate to the LXD API using bearer tokens

To authenticate to the LXD API using a bearer token, first create an identity of type `bearer`:

`````{tabs}
```{group-tab} CLI
    lxc auth identity create bearer/<name> [[--group <group> ]]
```
```{group-tab} API
    lxc query --request POST /1.0/auth/identities/bearer --data '{
      "name": "<name>",
      "type": "bearer",
      "groups": [
        "<group>"
      ]
    }'
```
`````

Next, issue a token for the identity:

`````{tabs}
```{group-tab} CLI
    lxc auth identity token issue bearer/<name> [--expiry <expiry> ]
```
```{group-tab} API
    lxc query --request POST /1.0/auth/identities/bearer/<name>/token --data '{
      "expiry": "<expiry>"
    }'
```
`````

The returned token can be used to authenticate with LXD.
It must be set as a bearer token in the `Authorization` header.

You can verify trust by checking the `auth` field in the response metadata of `GET /1.0`:

```
$ curl -k -H "Authorization: Bearer ${TOKEN}" https://<lxd_address>/1.0
{
  ...
  "metadata": {
    "auth":"trusted"
  }
}
```
