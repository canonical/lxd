---
discourse: 13114
relatedlinks: https://www.youtube.com/watch?v=6O0q3rSWr8A
---

# Remote API authentication

Remote communications with the LXD daemon happen using JSON over HTTPS.

To be able to access the remote API, clients must authenticate with the LXD server.
The following authentication methods are supported:

- {ref}`authentication-tls-certs`
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

The supported protocol must be TLS 1.2 or better.
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

To allow establishing a new trust relationship from the client side, you must set a trust password (`core.trust_password`, see {doc}`server`) for the server. Clients can then add their own certificate to the server's trust store by providing the trust password when prompted.

In a production setup, unset `core.trust_password` after all clients have been added.
This prevents brute-force attacks trying to guess the password.

(authentication-token)=
#### Adding client certificates using tokens

You can also add new clients by using tokens. This is a safer way than using the trust password, because tokens expire once they've been used.

To use this method, generate a token for each client by calling `lxc config trust add`, which will prompt for the client name.
The clients can then add their certificates to the server's trust store by providing the generated token when prompted for the trust password.
Alternatively, the clients can provide the token directly when adding the remote: `lxc remote add <name> <token>`.

### Using a PKI system

In a {abbr}`PKI (Public key infrastructure)` setup, a system administrator manages a central PKI that issues client certificates for all the lxc clients and server certificates for all the LXD daemons.

To enable PKI mode, complete the following steps:

1. Add the {abbr}`CA (Certificate authority)` certificate to all machines:

   - Place the `client.ca` file in the clients' configuration directories (`~/.config/lxc`).
   - Place the `server.ca` file in the server's configuration directory (`/var/lib/lxd` or `/var/snap/lxd/common/lxd` for snap users).
1. Place the certificates issued by the CA on the clients and the server, replacing the automatically generated ones.
1. Restart the server.

In that mode, any connection to a LXD daemon will be done using the
preseeded CA certificate.

If the server certificate isn't signed by the CA, the connection will simply go through the normal authentication mechanism.
If the server certificate is valid and signed by the CA, then the connection continues without prompting the user for the certificate.

Note that the generated certificates are not automatically trusted. You must still add them to the server in one of the ways described in {ref}`authentication-trusted-clients`.

(authentication-candid)=
## Candid-based authentication

```{youtube} https://www.youtube.com/watch?v=FebTipM1jJk
```

When LXD is configured to use [Candid](https://github.com/canonical/candid) authentication, clients that try to authenticate with the server must get a Discharge token from the authentication server specified by the `candid.api.url` setting (see {doc}`server`).

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

LXD supports integrating with the Canonical RBAC service.
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

In a {ref}`restricted project <projects-restrictions>`, the `operator` role is safe to use as well if configured appropriately.
```

## Failure scenarios

In the following scenarios, authentication is expected to fail.

### Server certificate changed

The server certificate might change in the following cases:

 * The server was fully reinstalled and therefore got a new certificate.
 * The connection is being intercepted ({abbr}`MITM (Machine in the middle)`).

In such cases, the client will refuse to connect to the server because the certificate fingerprint does not match the fingerprint in the configuration for this remote.

It is then up to the user to contact the server administrator to check if the certificate did in fact change.
If it did, the certificate can be replaced by the new one, or the remote can be removed altogether and re-added.

### Server trust relationship revoked

The server trust relationship is revoked for a client if another trusted client or the local server administrator removes the trust entry for the client on the server.

In this case, the server still uses the same certificate, but all API calls return a 403 code with an error indicating that the client isn't trusted.
