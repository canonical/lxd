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

The new identity is reported with the type `Client token bearer (pending)`, which means that no token has been issued for it yet.
Such an identity cannot authenticate, although it can already be added to groups.

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

The `expiry` field accepts multiple space-separated values of the form `<number><unit>`, such as `1d 3H 5M` (1 day, 3 hours, and 5 minutes).
Case-sensitive units: years (`y`), months (`m`), weeks (`w`), days (`d`), hours (`H`), minutes (`M`), and seconds (`S`).

Note the distinction between months (`m`) and minutes (`M`): for example, `1m` means one month, while `1M` means one minute.

The returned token can be used to authenticate with LXD.
It must be set as a bearer token in the `Authorization` header.

You can verify trust by checking the `auth` field in the response metadata of `GET /1.0`:

```bash
$ curl -k -H "Authorization: Bearer ${TOKEN}" https://<lxd_address>/1.0
{
  ...
  "metadata": {
    "auth":"trusted"
  }
}
```

Finally, a token that is no longer needed can be revoked:

`````{tabs}
```{group-tab} CLI
    lxc auth identity token revoke bearer/<name>
```
```{group-tab} API
    lxc query --request DELETE /1.0/auth/identities/bearer/<name>/token
```
`````

Revoking the token invalidates it immediately and returns the identity to the pending state, where it stays until a new token is issued for it.
Any groups the identity belongs to are retained, so a newly issued token grants the same permissions as the revoked one.
