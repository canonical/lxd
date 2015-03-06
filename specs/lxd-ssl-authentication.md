# Introduction
Local communications over the UNIX socket happen over a cleartext HTTP
socket and access is restricted by socket ownership and mode.

Remote communications with the LXD daemon happen using JSON over HTTPS.
The supported protocol must be TLS1.2 or better.
All communications must use perfect forward secrecy and ciphers must be
limited to strong elliptic curve ones (such as ECDHE-RSA or
ECDHE-ECDSA).

Any generated key should be at least 4096bit RSA and when using
signatures, only SHA-2 signatures should be trusted.

Since we control both client and server, there is no reason to support
any backward compatibility to broken protocol or ciphers.

Both the client and the server will generate a keypair the first time
they're launched. The server will use that for all https connections to
the LXD socket and the client will use its certificate as a client
certificate for any client-server communication.

# Adding a remote with a default setup
In the default setup, when the user adds a new server with "lxc remote
add", the server will be contacted over HTTPs, its certificate
downloaded and the fingerprint will be shown to the user.

The user will then be asked to confirm that this is indeed the server's
fingerprint which they can manually check by connecting to or asking
someone with access to the server to run the status command and compare
the fingerprints.

After that, the user must enter the trust password for that server, if
it matches, the client certificate is added to the server's trust store
and the client can now connect to the server without having to provide
any additional credentials.

This is a workflow that's very similar to that of ssh where an initial
connection to an unknown server triggers a prompt.

A possible extension to that is to support something similar to ssh's
fingerprint in DNS feature where the certificate fingerprint is added as
a TXT record, then if the domain is signed by DNSSEC, the client will
automatically accept the fingerprint if it matches that in the DNS
record.

# Adding a remote with a PKI based setup
In the PKI setup, a system administrator is managing a central PKI, that
PKI then issues client certificates for all the lxc clients and server
certificates for all the LXD daemons.

Those certificates and keys are manually put in place on the various
machines, replacing the automatically generated ones.

The CA certificate is also added to all lxc clients and LXD daemons.
A CRL may also accompany the CA certificate.

In that mode, any connection to a LXD daemon will be done using the
preseeded CA certificate. If the server certificate isn't signed by the
CA, or if it has been revoked, the connection will simply fail with no
way obvious way for the user to bypass this.

If the server certificate is valid and signed by the CA, then the
connection continues without prompting the user for the certificate.

After that, the user must enter the trust password for that server, if
it matches, the client certificate is added to the server's trust store
and the client can now connect to the server without having to provide
any additional credentials.

# Password prompt
To establish a new trust relationship, a password must be set on the
server and send by the client when adding itself.

A remote add operation should therefore go like this:
 1. Call GET /1.0
 2. If we're not in a PKI setup with a ca.crt, ask the user to confirm the fingerprint.
 3. Look at the dict we received back from the server. If "auth" is
    "untrusted", ask the user for the server's password and do a POST to
    /1.0/certificates, then call /1.0 again to check that we're indeed
    trusted.
 4. Remote is now ready

# Failure scenarios
## Server certificate changes
This will typically happen in two cases:

 * The server was fully reinstalled and so changed certificate
 * The connection is being intercepted (MITM)

In such cases the client will refuse to connect to the server since the
certificate fringerprint will not match that in the config for this
remote.

This is a fatal error and so the client shouldn't attempt to recover
from it. Instead it must print a message to the console saying that the
server certificate changed and that this may either be due to the server
having been reinstalled or because the communication is being
intercepted.

That message can also tell the user that if this is expected, they can
resolve the situation by removing the remote and adding it again (the
message should include the two commands required to achieve that in a
copy/pastable manner).


## Server trust relationship revoked
In this case, the server still uses the same certificate but all API
calls return a 403 with an error indicating that the client isn't
trusted.

This happens if another trusted client or the local server administrator
removed the trust entry on the server.

As with the other failure scenario, this is a fatal error. A message
must be displayed to the user explaining that this client isn't trusted
by the server and that to re-establish the trust relationship, the user
must remove the remote and add it again (and as above, provide the
commands to do so).
