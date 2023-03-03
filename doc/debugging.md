# Debugging

For information on debugging instance issues, see {ref}`instances-troubleshoot`.

## Debugging `lxc` and `lxd`

Here are different ways to help troubleshooting `lxc` and `lxd` code.

### `lxc --debug`

Adding `--debug` flag to any client command will give extra information
about internals. If there is no useful info, it can be added with the
logging call:

    logger.Debugf("Hello: %s", "Debug")

### `lxc monitor`

This command will monitor messages as they appear on remote server.

## REST API through local socket

On server side the most easy way is to communicate with LXD through
local socket. This command accesses `GET /1.0` and formats JSON into
human readable form using [jq](https://stedolan.github.io/jq/tutorial/)
utility:

```bash
curl --unix-socket /var/lib/lxd/unix.socket lxd/1.0 | jq .
```

or for snap users:

```bash
curl --unix-socket /var/snap/lxd/common/lxd/unix.socket lxd/1.0 | jq .
```

See the [RESTful API](rest-api.md) for available API.

## REST API through HTTPS

[HTTPS connection to LXD](security.md) requires valid
client certificate that is generated on first `lxc remote add`. This
certificate should be passed to connection tools for authentication
and encryption.

If desired, `openssl` can be used to examine the certificate (`~/.config/lxc/client.crt`
or `~/snap/lxd/common/config/client.crt` for snap users):

```bash
openssl x509 -text -noout -in client.crt
```

Among the lines you should see:

    Certificate purposes:
    SSL client : Yes

### With command line tools

```bash
wget --no-check-certificate --certificate=$HOME/.config/lxc/client.crt --private-key=$HOME/.config/lxc/client.key -qO - https://127.0.0.1:8443/1.0

# or for snap users
wget --no-check-certificate --certificate=$HOME/snap/lxd/common/config/client.crt --private-key=$HOME/snap/lxd/common/config/client.key -qO - https://127.0.0.1:8443/1.0
```

### With browser

Some browser plugins provide convenient interface to create, modify
and replay web requests. To authenticate against LXD server, convert
`lxc` client certificate into importable format and import it into
browser.

For example this produces `client.pfx` in Windows-compatible format:

```bash
openssl pkcs12 -clcerts -inkey client.key -in client.crt -export -out client.pfx
```

After that, opening [`https://127.0.0.1:8443/1.0`](https://127.0.0.1:8443/1.0) should work as expected.
