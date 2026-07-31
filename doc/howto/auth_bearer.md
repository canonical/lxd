(howto-auth-bearer)=
# How to authenticate to the LXD API using bearer tokens

To authenticate to the LXD API using a bearer token, first create an identity of type `bearer`:

`````{tabs}
````{group-tab} CLI
```bash
lxc auth identity create bearer/<name> [[--group <group> ]]
```

The new identity initially has type `Client token bearer (pending)`.
A pending identity cannot authenticate but can be added to groups.

Next, issue a token for the identity (this changes its type to `Client token bearer`):
```bash
lxc auth identity token issue bearer/<name> [--expiry <expiry> ]
```
````

````{group-tab} API
```bash
lxc query --request POST /1.0/auth/identities/bearer --data '{
  "name": "<name>",
  "type": "bearer",
  "groups": [
    "<group>"
  ]
}'
```

Next, issue a token for the identity:
```bash
lxc query --request POST /1.0/auth/identities/bearer/<name>/token --data '{
  "expiry": "<expiry>"
}'
```
````

````{group-tab} UI
Click {guilabel}`Permissions` in the navigation sidebar, then select {guilabel}`Identities` from the expanded drop-down list.

Click on the {guilabel}`+ Create identity` button to open the side panel.

Select {guilabel}`Bearer token (Main API)`. Enter a name and optionally a token expiry for the new identity. Select relevant authentication group(s), then click {guilabel}`Create identity`.

In the modal, click the copy button {{copy_button}} to copy the token.
````
`````

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

```{note}
The `expiry` field accepts multiple space-separated values of the form `<number><unit>`, such as `1d 3H 5M` (1 day, 3 hours, and 5 minutes).
Case-sensitive units: years (`y`), months (`m`), weeks (`w`), days (`d`), hours (`H`), minutes (`M`), and seconds (`S`).

Note the distinction between months (`m`) and minutes (`M`): for example, `1m` means one month, while `1M` means one minute.
```

Finally, a token that is no longer needed can be revoked:

`````{tabs}
````{group-tab} CLI
```bash
lxc auth identity token revoke bearer/<name>
```
````
````{group-tab} API
```bash
lxc query --request DELETE /1.0/auth/identities/bearer/<name>/token
```
````
`````

Revoking the token invalidates it immediately and returns the identity to the pending state.
The identity has type `Client token bearer (pending)` until a new token is issued for it.
The identity remains in its assigned groups, so a newly issued token grants its bearer the same permissions as the revoked one.
