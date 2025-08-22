---
discourse: lxc:[Token&#32;based&#32;remote&#32;connection](13114),lxc:[ACME&#32;support&#32;for&#32;server&#32;certificate](15142)
relatedlinks: "[LXD&#32;for&#32;multi-user&#32;systems&#32;-&#32;YouTube](https://www.youtube.com/watch?v=6O0q3rSWr8A)"
---

(authentication)=
# Remote API authentication

Remote communications with the LXD daemon happen using JSON over HTTPS.
This requires the LXD API to be exposed over the network; see {ref}`server-expose` for instructions.

To be able to access the remote API, clients must authenticate with the LXD server.
The following authentication methods are supported:

- {ref}`authentication-tls-certs`
- {ref}`authentication-openid`

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

All communications must use perfect forward secrecy, and ciphers must be limited to strong elliptic curve ones (such as ECDHE-RSA or ECDHE-ECDSA).

Any generated key should be at least 4096 bit RSA, preferably 384 bit ECDSA.
When using signatures, only SHA-2 signatures should be trusted.

Since we control both client and server, there is no reason to support
any backward compatibility to broken protocol or ciphers.

(authentication-trusted-clients)=
### Trusted TLS clients

The workflow to authenticate with the server is similar to that of SSH, where an initial connection to an unknown server triggers a prompt:

1. When the user adds a server with [`lxc remote add`](lxc_remote_add.md), the server is contacted over HTTPS, its certificate is downloaded and the fingerprint is shown to the user.
1. The user is asked to confirm that this is indeed the server's fingerprint, which they can manually check by connecting to the server or by asking someone with access to the server to run the info command and compare the fingerprints.
1. The server attempts to authenticate the client:

   - If the client certificate is in the server's trust store, the connection is granted.
   - If the client certificate is not in the server's trust store, the server prompts the user for a token.
     If the provided token matches, the client certificate is added to the server's trust store and the connection is granted.
     Otherwise, the connection is rejected.

See {ref}`server-expose` and {ref}`server-authenticate` for instructions on how to configure TLS authentication and add trusted clients.

(authentication-pki)=
### Using a PKI system

In a {abbr}`PKI (Public key infrastructure)` setup, a system administrator manages a central PKI that issues client certificates for all the LXD clients and server certificates for all the LXD daemons.

In PKI mode, TLS authentication requires that client certificates are signed be the {abbr}`CA (Certificate authority)`.
This requirement does not apply to clients that authenticate via [OIDC](authentication-openid).

The steps for enabling PKI mode differ slightly depending on whether you use an ACME provider in addition (see {ref}`authentication-server-certificate`).

`````{tabs}
````{group-tab} Only PKI
If you use a PKI system, both the server and client certificates are issued by intermediate CA(s).
The `client.ca` file contains the certificate used by the client to verify the server certificate it receives when making a connection to a remote.
The `server.ca` file contains the certificate used by the server to verify the client certificate associated with an incoming connection.

Both files contain trust anchors used to evaluate if the received leaf certificate from the other end of the connection is to be trusted or not.
If the leaf certificate's chain of trust leads to one of the trusted anchors it will be trusted (unless revoked).

1. Add the CA certificate to all machines:
   - Place the `client.ca` file in the clients' configuration directories (`~/.config/lxc` or `~/snap/lxd/common/config` for snap users).
   - Place the `server.ca` file in the server's configuration directory (`/var/lib/lxd` or `/var/snap/lxd/common/lxd` for snap users).

     ```{note}
     In a cluster setup, the CA certificate  must be named `cluster.ca`, and the same file must be added to all cluster members.
     ```

1. Place the certificates issued by the CA in the clients' configuration directories, replacing the automatically generated `client.crt` and `client.key` files.
1. If you want clients to automatically trust the server, place the certificates issued by the CA in the server's configuration directory, replacing the automatically generated `server.crt` and `server.key` files.

   ```{note}
   In a cluster setup, the certificate files must be named `cluster.crt` and `cluster.key`.
   They must be identical on all cluster members.
   ```

   When a client adds a PKI-enabled server or cluster as a remote, it checks the server certificate and prompts the user to trust the server certificate only if the certificate has not been signed by the CA.
1. Restart the LXD daemon.
````
````{group-tab} PKI and ACME
If you use a PKI system alongside an ACME provider, the server certificates are issued by the ACME provider, and the client certificates are issued by a secondary CA.

1. Place the CA certificate for the server (`server.ca`) in the server's configuration directory (`/var/lib/lxd` or `/var/snap/lxd/common/lxd` for snap users), so that the server can authenticate the clients.

   ```{note}
   In a cluster setup, the CA certificate  must be named `cluster.ca`, and the same file must be added to all cluster members.
   ```

1. Place the certificates issued by the CA in the clients' configuration directories, replacing the automatically generated `client.crt` and `client.key` files.
1. Restart the LXD daemon.

````
`````

#### Trusting certificates

CA-signed client certificates are not automatically trusted.
You must still add them to the server in one of the ways described in {ref}`authentication-trusted-clients`.

To automatically trust CA-signed client certificates, set the {config:option}`server-core:core.trust_ca_certificates` server configuration to true.
When `core.trust_ca_certificates` is enabled, any new clients with a CA-signed certificate will have full access to LXD.

#### Revoking certificates

To revoke certificates via the PKI, place a certificate revocation list in the server's configuration directory as `ca.crl` and restart the LXD daemon.
A client with a CA-signed certificate that has been revoked, and is present in `ca.crl`, will not be able to authenticate with LXD, nor add LXD as a remote via [mutual TLS](authentication-trusted-clients).

(authentication-openid)=
## OpenID Connect authentication

LXD supports using [OpenID Connect](https://openid.net/developers/how-connect-works/) to authenticate users through an {abbr}`OIDC (OpenID Connect)` Identity Provider.

To configure LXD to use OIDC authentication, set the [`oidc.*`](server-options-oidc) server configuration options.
Your OIDC provider must be configured to enable the [Device Authorization Grant](https://oauth.net/2/device-flow/) type.

To add a remote pointing to a LXD server configured with OIDC authentication, run [`lxc remote add <remote_name> <remote_address>`](lxc_remote_add.md).
You are then prompted to authenticate through your web browser, where you must confirm that the device code displayed in the browser matches the device code that is displayed in the terminal window.
The LXD client then retrieves and stores an access token, which it provides to LXD for all interactions.
The identity provider might also provide a refresh token.
In this case, the LXD client uses this refresh token to attempt to retrieve another access token when the current access token has expired.

```{warning}
Only set `oidc.client.secret` if required by the Identity Provider. Once set, this key allows the LXD UI client to authenticate.
However, the secret is not shared with other LXD clients (such as the LXD CLI).
```

When an OIDC client initially authenticates with LXD, it does not have access to the majority of the LXD API.
OIDC clients must be granted access by an administrator, see {ref}`fine-grained-authorization`.

(authentication-server-certificate)=
## TLS server certificate

LXD supports issuing server certificates using {abbr}`ACME (Automatic Certificate Management Environment)` services, for example, [Let's Encrypt](https://letsencrypt.org/).

To enable this feature, set the following server configuration:

- {config:option}`server-acme:acme.domain`: The domain for which the certificate should be issued.
- {config:option}`server-acme:acme.email`: The email address used for the account of the ACME service.
- {config:option}`server-acme:acme.agree_tos`: Must be set to `true` to agree to the ACME service's terms of service.
- {config:option}`server-acme:acme.ca_url`: The directory URL of the ACME service. By default, LXD uses "Let's Encrypt".

LXD currently only supports the [`HTTP-01 challenge`](https://letsencrypt.org/docs/challenge-types/#http-01-challenge), which requires handling incoming HTTP requests on port 80.
This can be achieved by using a reverse proxy such as [HAProxy](https://www.haproxy.org/).

The HAProxy configuration example below uses `lxd.example.net` as the domain.
After the certificate has been issued, LXD will be reachable from `https://lxd.example.net/`.
It applies filtering to minimize the amount of undesired traffic coming from the internet reaching the protected LXD cluster.

```
# HAProxy
global
  log /dev/log local0
  log /dev/log local1 notice
  chroot /var/lib/haproxy
  stats socket /run/haproxy/admin.sock mode 660 level admin
  stats timeout 30s
  user haproxy
  group haproxy
  daemon
  maxconn 100000

defaults
  mode tcp
  log global
  option tcplog
  option dontlognull
  timeout connect 5s
  timeout client 30s
  timeout client-fin 30s
  timeout server 30s
  timeout tunnel 300s
  timeout http-request 5s
  timeout check 5s
  maxconn 80000

# Frontend for HTTP traffic - HTTP mode for ACME challenges redirection
frontend http_frontend
  bind *:80
  mode http
  option httplog

  # ACME challenges are very low traffic even with MPIC
  # (Multi-Perspective Issuance Corroboration) validation.
  maxconn 32

  # Only redirect ACME challenges for known hosts to HTTPS
  http-request deny unless { hdr(host) lxd.example.com }
  http-request deny unless { path_beg /.well-known/acme-challenge/ }
  redirect scheme https code 301

# Frontend for HTTPS traffic - TCP mode with SNI inspection
frontend https_frontend
  bind *:443

  # TCP request inspection for SNI and client filtering
  tcp-request inspect-delay 5s

  # Extract SNI from TLS handshake
  tcp-request content capture req.ssl_sni len 64

  # Reject unwanted traffic
  # non-TLS
  tcp-request content reject unless { req.ssl_hello_type 1 }

  # for unknown SNI hosts
  tcp-request content reject unless { req.ssl_sni lxd.example.com }

  # using too old TLS version
  # TLS 1.3 (SSL version 3.4) but it is hard to distinguish TLS 1.2
  # from 1.3 as TLS 1.3 tries to masquerade as a resumed TLS 1.2
  # connection to work around broken middleboxes. Reject anything
  # older than TLS 1.2.
  # See https://datatracker.ietf.org/doc/html/rfc8446#appendix-D.4
  tcp-request content reject if { req.ssl_ver lt 3.3 }

  # Rate limiting for LXD traffic (that passed above checks)
  stick-table type ip size 100k expire 30s store conn_rate(10s)
  tcp-request content track-sc0 src
  tcp-request content reject if { sc_conn_rate(0) gt 50 }

  # Route to backend
  default_backend lxd_cluster_tcp

# Additional frontend for LXD management on different port (optional)
frontend lxd_management
  bind *:8443

  # Network restrictions (only allow trusted networks)
  tcp-request connection reject unless { src 192.0.2.0/24 }

  # Route to backend
  default_backend lxd_cluster_tcp

# Backend for LXD cluster (TCP mode with TLS passthrough)
backend lxd_cluster_tcp
  balance roundrobin

  # Sticky sessions based on TLS session ID (extracted from handshake)
  stick-table type binary len 32 size 30k expire 30m
  acl clienthello req_ssl_hello_type 1
  acl serverhello rep_ssl_hello_type 2
  # use tcp content accepts to detects ssl client and server hello.
  tcp-request inspect-delay 5s
  tcp-request content accept if clienthello
  # no timeout on response inspect delay by default.
  tcp-response content accept if serverhello
  # SSL session ID (SSLID) may be present on a client or server hello.
  # Its length is coded on 1 byte at offset 43 and its value starts
  # at offset 44.
  # Match and learn on request if client hello.
  stick on payload_lv(43,1) if clienthello
  # Learn on response if server hello.
  stick store-response payload_lv(43,1) if serverhello

  # Health checks using simple TCP connect
  option tcp-check

  # Failed connections will be redispatched to another cluster member
  option redispatch

  # LXD cluster members with PROXY protocol and core.https_trusted_proxy
  server lxd-1 1.2.3.4:8443 check send-proxy
  server lxd-2 1.2.3.5:8443 check send-proxy
  server lxd-3 1.2.3.6:8443 check send-proxy
# EOF
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

## Related topics

{{security_exp}}

{{security_how}}
