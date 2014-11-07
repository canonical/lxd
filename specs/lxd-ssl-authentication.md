# Introduction
Remote communications with the lxd daemon happen using JSON over HTTPS.
The only supported protocol should be TLS1.2 with perfect
forward secrecy, ciphers should be limited to strong elliptic curve ones
(such as ECDHE-RSA or ECDHE-ECDSA).

Since we control both client and server, there is no reason to support
any backward compatibility to broken protocol or ciphers.

Both the client and the server will generate a keypair the first time
they're launched. The server will use that for all connections to the
https+lxd socket and the client will use its certificate as a client
certificate for any client-server communication.

# Adding a remote with a default setup
In the default setup, when the user adds a new server with "lxc remote
add", the server will be contacted over HTTPs, its certificate
downloaded and the fingerprint will be shown to the user.

The user will then be asked to confirm that this is indeed the server's
fingerprint which they can manually check by connecting to or asking
someone with access to the server to run the status command and compare
the fingerprints.

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
certificates for all the lxd daemons.

Those certificates and keys are manually put in place on the various
machines, replacing the automatically generated ones.

The CA certificate is also added to all lxc clients and lxd daemons.
A CRL may also accompany the CA certificate.

In that mode, any connection to a lxd daemon will be done using the
preseed CA certificate. If the server certificate isn't signed by the
CA, or if it has been revoked, the connection will simply fail with no
way obvious way for the user to bypass this.

If the server certificate is valid and signed by the CA, then the
connection continues without ever prompting the user.
