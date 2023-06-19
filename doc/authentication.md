---
discourse: 13114,15142
relatedlinks: https://www.youtube.com/watch?v=6O0q3rSWr8A
---

(authentication)=
# Remote API authentication

Remote communications with the LXD daemon happen using JSON over HTTPS.

To be able to access the remote API, clients must authenticate with the LXD server.
The following authentication methods are supported:

- {ref}`authentication-tls-certs`
- {ref}`authentication-openid`
- {ref}`authentication-candid`
- {ref}`authentication-rbac`

(authentication-tls-certs)=
## TLS client certificates

```{youtube} https://www.youtube.com/watch?v=4iNpiL-lrXU
```

When using {abbr}`TLS (Transport Layer Security)` client certificates for authentication, both the client and the server will generate a key pair the first time they're launched.
The server will use that key pair for all HTTPS connections to the LXD socket.
The client will use its certificate as a client certificate for any client-server communication.

To cause certificates to be regenerated, simply remove the old ones.
On the next connection, a new certificate is generated.

### Communication protocol

The supported protocol must be TLS 1.3 or better.

It's possible to force LXD to accept TLS 1.2 by setting the `LXD_INSECURE_TLS` environment variable on both client and server.
However this isn't a supported setup and should only ever be used when forced to use an outdated corporate proxy.

All communications must use perfect forward secrecy, and ciphers must be limited to strong elliptic curve ones (such as ECDHE-RSA or ECDHE-ECDSA).

Any generated key should be at least 4096 bit RSA, preferably 384 bit ECDSA.
When using signatures, only SHA-2 signatures should be trusted.

Since we control both client and server, there is no reason to support
any backward compatibility to broken protocol or ciphers.

(authentication-trusted-clients)=
### Trusted TLS clients

You can obtain the list of TLS certificates trusted by a LXD server with `lxc config trust list`.

Trusted clients can be added in either of the following ways:

- {ref}`authentication-add-certs`
- {ref}`authentication-trust-pw`
- {ref}`authentication-token`

The workflow to authenticate with the server is similar to that of SSH, where an initial connection to an unknown server triggers a prompt:

1. When the user adds a server with `lxc remote add`, the server is contacted over HTTPS, its certificate is downloaded and the fingerprint is shown to the user.
1. The user is asked to confirm that this is indeed the server's fingerprint, which they can manually check by connecting to the server or by asking someone with access to the server to run the info command and compare the fingerprints.
1. The server attempts to authenticate the client:

   - If the client certificate is in the server's trust store, the connection is granted.
   - If the client certificate is not in the server's trust store, the server prompts the user for a token or the trust password.
     If the provided token or trust password matches, the client certificate is added to the server's trust store and the connection is granted.
     Otherwise, the connection is rejected.

To revoke trust to a client, remove its certificate from the server with `lxc config trust remove FINGERPRINT`.

It's possible to restrict a TLS client to one or multiple projects.
In this case, the client will also be prevented from performing global configuration changes or altering the configuration (limits, restrictions) of the projects it's allowed access to.

To restrict access, use `lxc config trust edit FINGERPRINT`.
Set the `restricted` key to `true` and specify a list of projects to restrict the client to.
If the list of projects is empty, the client will not be allowed access to any of them.

(authentication-add-certs)=
#### Adding trusted certificates to the server

The preferred way to add trusted clients is to directly add their certificates to the trust store on the server.
To do so, copy the client certificate to the server and register it using `lxc config trust add <file>`.

(authentication-trust-pw)=
#### Adding client certificates using a trust password

To allow establishing a new trust relationship from the client side, you must set a trust password ([`core.trust_password`](server-options-core)) for the server. Clients can then add their own certificate to the server's trust store by providing the trust password when prompted.

In a production setup, unset `core.trust_password` after all clients have been added.
This prevents brute-force attacks trying to guess the password.

(authentication-token)=
#### Adding client certificates using tokens

You can also add new clients by using tokens. This is a safer way than using the trust password, because tokens expire after a configurable time ([`core.remote_token_expiry`](server-options-core)) or once they've been used.

To use this method, generate a token for each client by calling `lxc config trust add`, which will prompt for the client name.
The clients can then add their certificates to the server's trust store by providing the generated token when prompted for the trust password.

<!-- Include start NAT authentication -->

```{note}
If your LXD server is behind NAT, you must specify its external public address when adding it as a remote for a client:

    lxc remote add <name> <IP_address>

When you are prompted for the admin password, specify the generated token.

When generating the token on the server, LXD includes a list of IP addresses that the client can use to access the server.
However, if the server is behind NAT, these addresses might be local addresses that the client cannot connect to.
In this case, you must specify the external address manually.
```

<!-- Include end NAT authentication -->

Alternatively, the clients can provide the token directly when adding the remote: `lxc remote add <name> <token>`.

### Using a PKI system

In a {abbr}`PKI (Public key infrastructure)` setup, a system administrator manages a central PKI that issues client certificates for all the LXD clients and server certificates for all the LXD daemons.

To enable PKI mode, complete the following steps:

1. Add the {abbr}`CA (Certificate authority)` certificate to all machines:

   - Place the `client.ca` file in the clients' configuration directories (`~/.config/lxc` or `~/snap/lxd/common/config` for snap users).
   - Place the `server.ca` file in the server's configuration directory (`/var/lib/lxd` or `/var/snap/lxd/common/lxd` for snap users).
1. Place the certificates issued by the CA on the clients and the server, replacing the automatically generated ones.
1. Restart the server.

In that mode, any connection to a LXD daemon will be done using the
pre-seeded CA certificate.

If the server certificate isn't signed by the CA, the connection will simply go through the normal authentication mechanism.
If the server certificate is valid and signed by the CA, then the connection continues without prompting the user for the certificate.

Note that the generated certificates are not automatically trusted. You must still add them to the server in one of the ways described in {ref}`authentication-trusted-clients`.

(authentication-openid)=
## OpenID Connect authentication

LXD supports using [OpenID Connect](https://openid.net/connect/) to authenticate users through an {abbr}`OIDC (OpenID Connect)` Identity Provider.

```{note}
OpenID Connect authentication is currently under development.
Starting with LXD 5.13, authentication through OpenID Connect is supported, but there is no user role handling in place so far.
Any user that authenticates through the configured OIDC Identity Provider gets full access to LXD.
```

To configure LXD to use OIDC authentication, set the [`oidc.*`](server-options-oidc) server configuration options.

To add a remote pointing to a LXD server configured with OIDC authentication, run `lxc remote add <remote_name> <remote_address>`.
You are then prompted to authenticate through your web browser, where you must confirm the device code that LXD uses.
The LXD client then retrieves and stores the access and refresh tokens and provides those to LXD for all interactions.

(authentication-candid)=
## Candid-based authentication

```{youtube} https://www.youtube.com/watch?v=FebTipM1jJk
```

You can configure LXD to use [Candid](https://github.com/canonical/candid) authentication by setting the [`candid.*`](server-options-candid-rbac) server configuration options.
In this case, clients that try to authenticate with the server must get a Discharge token from the authentication server specified by the [`candid.api.url`](server-options-candid-rbac) option.

The authentication server certificate must be trusted by the LXD server.

To add a remote pointing to a LXD server configured with Candid/Macaroon authentication, run `lxc remote add REMOTE ENDPOINT --auth-type=candid`.
To verify the user, the client will prompt for the credentials required by the authentication server.
If the authentication is successful, the client will connect to the LXD server and present the token received from the authentication server.
The LXD server verifies the token, thus authenticating the request.
The token is stored as cookie and is presented by the client at each request to LXD.

For instructions on how to set up Candid-based authentication, see the [Candid authentication for LXD](https://ubuntu.com/tutorials/candid-authentication-lxd) tutorial.

(authentication-rbac)=
## Role Based Access Control (RBAC)

```{youtube} https://www.youtube.com/watch?v=VE60AbJHT6E
```

LXD supports integrating with the Canonical RBAC service, which is included in the [Ubuntu Pro](https://ubuntu.com/pro) subscription.
Combined with Candid-based authentication, {abbr}`RBAC (Role Based Access Control)` can be used to limit what an API client is allowed to do on LXD.

In such a setup, authentication happens through Candid, while the RBAC service maintains roles to user/group relationships.
Roles can be assigned to individual projects, to all projects or to the entire LXD instance.

The meaning of the roles when applied to a project is as follows:

- auditor: Read-only access to the project
- user: Ability to do normal life cycle actions (start, stop, ...),
        execute commands in the instances, attach to console, manage snapshots, ...
- operator: All of the above + the ability to create, re-configure and
            delete instances and images
- admin: All of the above + the ability to reconfigure the project itself

```{important}
In an unrestricted project, only the `auditor` and the `user` roles are suitable for users that you wouldn't trust with root access to the host.

In a {ref}`restricted project <project-restrictions>`, the `operator` role is safe to use as well if configured appropriately.
```

To enable RBAC for your LXD server, set the [`rbac.*`](server-options-candid-rbac) server configuration options, which are a superset of the `candid.*` ones and allow for LXD to integrate with the RBAC service.

(authentication-server-certificate)=
## TLS server certificate

LXD supports issuing server certificates using {abbr}`ACME (Automatic Certificate Management Environment)` services, for example, [Let's Encrypt](https://letsencrypt.org/).

To enable this feature, set the following {ref}`server configuration <server-options-acme>`:

- `acme.domain`: The domain for which the certificate should be issued.
- `acme.email`: The email address used for the account of the ACME service.
- `acme.agree_tos`: Must be set to `true` to agree to the ACME service's terms of service.
- `acme.ca_url`: The directory URL of the ACME service. By default, LXD uses "Let's Encrypt".

For this feature to work, LXD must be reachable from port 80.
This can be achieved by using a reverse proxy such as [HAProxy](http://www.haproxy.org/).

Here's a minimal HAProxy configuration that uses `lxd.example.net` as the domain.
After the certificate has been issued, LXD will be reachable from `https://lxd.example.net/`.

```
# Global configuration
global
  log /dev/log local0
  chroot /var/lib/haproxy
  stats socket /run/haproxy/admin.sock mode 660 level admin
  stats timeout 30s
  user haproxy
  group haproxy
  daemon
  ssl-default-bind-options ssl-min-ver TLSv1.2
  tune.ssl.default-dh-param 2048
  maxconn 100000

# Default settings
defaults
  mode tcp
  timeout connect 5s
  timeout client 30s
  timeout client-fin 30s
  timeout server 120s
  timeout tunnel 6h
  timeout http-request 5s
  maxconn 80000

# Default backend - Return HTTP 301 (TLS upgrade)
backend http-301
  mode http
  redirect scheme https code 301

# Default backend - Return HTTP 403
backend http-403
  mode http
  http-request deny deny_status 403

# HTTP dispatcher
frontend http-dispatcher
  bind :80
  mode http

  # Backend selection
  tcp-request inspect-delay 5s

  # Dispatch
  default_backend http-403
  use_backend http-301 if { hdr(host) -i lxd.example.net }

# SNI dispatcher
frontend sni-dispatcher
  bind :443
  mode tcp

  # Backend selection
  tcp-request inspect-delay 5s

  # require TLS
  tcp-request content reject unless { req.ssl_hello_type 1 }

  # Dispatch
  default_backend http-403
  use_backend lxd-nodes if { req.ssl_sni -i lxd.example.net }

# LXD nodes
backend lxd-nodes
  mode tcp

  option tcp-check

  # Multiple servers should be listed when running a cluster
  server lxd-node01 1.2.3.4:8443 check
  server lxd-node02 1.2.3.5:8443 check
  server lxd-node03 1.2.3.6:8443 check
```

## Failure scenarios

In the following scenarios, authentication is expected to fail.

### Server certificate changed

The server certificate might change in the following cases:

- The server was fully reinstalled and therefore got a new certificate.
- The connection is being intercepted ({abbr}`MITM (Machine in the middle)`).

In such cases, the client will refuse to connect to the server because the certificate fingerprint does not match the fingerprint in the configuration for this remote.

It is then up to the user to contact the server administrator to check if the certificate did in fact change.
If it did, the certificate can be replaced by the new one, or the remote can be removed altogether and re-added.

### Server trust relationship revoked

The server trust relationship is revoked for a client if another trusted client or the local server administrator removes the trust entry for the client on the server.

In this case, the server still uses the same certificate, but all API calls return a 403 code with an error indicating that the client isn't trusted.
