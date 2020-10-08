# Security
## Introduction
LXD is a daemon running as root.

Access to that daemon is only possible over a local UNIX socket by default.
Through configuration, it's then possible to expose the same API over
the network on a TLS socket.

**WARNING**: Local access to LXD through the UNIX socket always grants
full access to LXD. This includes the ability to attach any filesystem
paths or devices to any instance as well as tweaking all security
features on instances. You should only give such access to someone who
you'd trust with root access to your system.

The remote API uses either TLS client certificates or Candid based
authentication. Canonical RBAC support can be used combined with Candid
based authentication to limit what an API client may do on LXD.

## TLS configuration
Remote communications with the LXD daemon happen using JSON over HTTPS.
The supported protocol must be TLS1.2 or better.

All communications must use perfect forward secrecy and ciphers must be
limited to strong elliptic curve ones (such as ECDHE-RSA or ECDHE-ECDSA).

Any generated key should be at least 4096bit RSA, preferably EC384 and
when using signatures, only SHA-2 signatures should be trusted.

Since we control both client and server, there is no reason to support
any backward compatibility to broken protocol or ciphers.

Both the client and the server will generate a keypair the first time
they're launched. The server will use that for all https connections to
the LXD socket and the client will use its certificate as a client
certificate for any client-server communication.

To cause certificates to be regenerated, simply remove the old ones. On the
next connection a new certificate will be generated.

## Role Based Access Control (RBAC)
LXD supports integrating with the Canonical RBAC service.

This uses Candid based authentication with the RBAC service maintaining
roles to user/group relationships. Roles can be assigned to individual
projects, to all projects or to the entire LXD instance.

The meaning of the roles when applied to a project is as follow:

 - auditor: Read-only access to the project
 - user: Ability to do normal lifecycle actions (start, stop, ...),
   execute commands in the instances, attach to console, manage snapshots, ...
 - operator: All of the above + the ability to create, re-configure and
   delete instances and images
 - admin: All of the above + the ability to reconfigure the project itself

**WARNING**: Of those roles, only `auditor` and `user` are currently
suitable for a user whom you wouldn't trust with root access to the
host.

## Container security
LXD containers can use a pretty wide range of features for security.

By default containers are `unprivileged`, meaning that they operate
inside a user namespace, restricting the abilities of users in the
container to that of regular users on the host with limited privileges
on the devices that the container owns.

If data sharing between containers isn't needed, it is possible to
enable `security.idmap.isolated` which will use non-overlapping uid/gid
maps for each container, preventing potential DoS attacks on other
containers.

LXD can also run `privileged` containers if you so wish, do note that
those aren't root safe and a user with root in such a container will be
able to DoS the host as well as find ways to escape confinement.

More details on container security and the kernel features we use can be found on the
[LXC security page](https://linuxcontainers.org/lxc/security/).

## Adding a remote with TLS client certificate authentication
In the default setup, when the user adds a new server with `lxc remote add`,
the server will be contacted over HTTPS, its certificate downloaded and the
fingerprint will be shown to the user.

The user will then be asked to confirm that this is indeed the server's
fingerprint which they can manually check by connecting to or asking
someone with access to the server to run the info command and compare
the fingerprints.

After that, the user must enter the trust password for that server, if
it matches, the client certificate is added to the server's trust store
and the client can now connect to the server without having to provide
any additional credentials.

This is a workflow that's very similar to that of SSH where an initial
connection to an unknown server triggers a prompt.

## Adding a remote with a TLS client in a PKI based setup
In the PKI setup, a system administrator is managing a central PKI, that
PKI then issues client certificates for all the lxc clients and server
certificates for all the LXD daemons.

Those certificates and keys are manually put in place on the various
machines, replacing the automatically generated ones.

The CA certificate is also added to all machines.

In that mode, any connection to a LXD daemon will be done using the
preseeded CA certificate. If the server certificate isn't signed by the
CA, the connection will simply go through the normal authentication
mechanism.

If the server certificate is valid and signed by the CA, then the
connection continues without prompting the user for the certificate.

After that, the user must enter the trust password for that server, if
it matches, the client certificate is added to the server's trust store
and the client can now connect to the server without having to provide
any additional credentials.

Enabling PKI mode is done by adding a client.ca file in the
client's configuration directory (`~/.config/lxc`) and a server.ca file in
the server's configuration directory (`/var/lib/lxd`). Then a client
certificate must be issued by the CA for the client and a server
certificate for the server. Those must then replace the existing
pre-generated files.

After this is done, restarting the server will have it run in PKI mode.

## Adding a remote with Candid authentication
When LXD is configured with Candid, it will request that clients trying to
authenticating with it get a Discharge token from the authentication server
specified by the `candid.api.url` setting.

The authentication server certificate needs to be trusted by the LXD server.

To add a remote pointing to a LXD configured with Macaroon auth, run `lxc
remote add REMOTE ENDPOINT --auth-type=candid`.  The client will prompt for
the credentials required by the authentication server in order to verify the
user. If the authentication is successful, it will connect to the LXD server
presenting the token received from the authentication server.  The LXD server
verifies the token, thus authenticating the request.  The token is stored as
cookie and is presented by the client at each request to LXD.

## Managing trusted TLS clients
The list of TLS certificates trusted by a LXD server can be obtained with
`lxc config trust list`.

Clients can manually be added using `lxc config trust add <file>`,
removing the need for a shared trust password by letting an existing
administrator add the new client certificate directly to the trust store.

To revoke trust to a client its certificate can be removed with `lxc config
trust remove FINGERPRINT`.

## Password prompt with TLS authentication
To establish a new trust relationship when not already setup by the
administrator, a password must be set on the server and sent by the
client when adding itself.

A remote add operation should therefore go like this:

 1. Call GET /1.0
 2. If we're not in a PKI setup ask the user to confirm the fingerprint.
 3. Look at the dict we received back from the server. If "auth" is
    "untrusted", ask the user for the server's password and do a `POST` to
    `/1.0/certificates`, then call `/1.0` again to check that we're indeed
    trusted.
 4. Remote is now ready

## Failure scenarios
### Server certificate changes
This will typically happen in two cases:

 * The server was fully reinstalled and so changed certificate
 * The connection is being intercepted (MITM)

In such cases the client will refuse to connect to the server since the
certificate fringerprint will not match that in the config for this
remote.

It is then up to the user to contact the server administrator to check
if the certificate did in fact change. If it did, then the certificate
can be replaced by the new one or the remote be removed altogether and
re-added.

### Server trust relationship revoked
In this case, the server still uses the same certificate but all API
calls return a 403 with an error indicating that the client isn't
trusted.

This happens if another trusted client or the local server administrator
removed the trust entry on the server.

## Production setup
For production setup, it's recommended that `core.trust_password` is unset
after all clients have been added.  This prevents brute-force attacks trying to
guess the password.

Furthermore, `core.https_address` should be set to the single address where the
server should be available (rather than any address on the host), and firewall
rules should be set to only allow access to the LXD port from authorized
hosts/subnets.

## Network security

### Bridged NIC security

The default networking mode in LXD is to provide a 'managed' private network bridge that each instance connects to.
In this mode, there is an interface on the host called `lxdbr0` that acts as the bridge for the instances.

The host runs an instance of `dnsmasq` for each managed bridge, which is responsible for allocating IP addresses
and providing both authoritative and recursive DNS services.

Instances using DHCPv4 will be allocated an IPv4 address and a DNS record will be created for their instance name.
This prevents instances from being able to spoof DNS records by providing false hostname info in the DHCP request.

The `dnsmasq` service also provides IPv6 router advertisement capabilities. This means that instances will auto
configure their own IPv6 address using SLAAC, so no allocation is made by `dnsmasq`. However instances that are
also using DHCPv4 will also get an AAAA DNS record created for the equivalent SLAAC IPv6 address.
This assumes that the instances are not using any IPv6 privacy extensions when generating IPv6 addresses.

In this default configuration, whilst DNS names cannot not be spoofed, the instance is connected to an Ethernet
bridge and can transmit any layer 2 traffic that it wishes, which means an untrusted instance can effectively do
MAC or IP spoofing on the bridge.

It is also possible in the default configuration for instances connected to the bridge to modify the LXD host's
IPv6 routing table by sending (potentially malicious) IPv6 router advertisements to the bridge. This is because
the `lxdbr0` interface is created with `/proc/sys/net/ipv6/conf/lxdbr0/accept_ra` set to `2` meaning that the
LXD host will accept router advertisements even though `forwarding` is enabled (see
https://www.kernel.org/doc/Documentation/networking/ip-sysctl.txt for more info).

However LXD offers several `bridged` NIC security features that can be used to control the type of traffic that
an instance is allowed to send onto the network. These NIC settings should be added to the profile that the
instance is using, or can be added to individual instances, as shown below.

The following security features are available for `bridged` NICs:

Key                      | Type      | Default           | Required  | Description
:--                      | :--       | :--               | :--       | :--
security.mac\_filtering  | boolean   | false             | no        | Prevent the instance from spoofing another's MAC address
security.ipv4\_filtering | boolean   | false             | no        | Prevent the instance from spoofing another's IPv4 address (enables mac\_filtering)
security.ipv6\_filtering | boolean   | false             | no        | Prevent the instance from spoofing another's IPv6 address (enables mac\_filtering)

One can override the default `bridged` NIC settings from the profile on a per-instance basis using:

```
lxc config device override <instance> <NIC> security.mac_filtering=true
```

Used together these features can prevent an instance connected to a bridge from spoofing MAC and IP addresses.
These are implemented using either `xtables` (iptables, ip6tables and ebtables) or `nftables`, depending on what is
available on the host.

It's worth noting that those options effectively prevent nested containers from using the parent network with a
different MAC address (i.e using bridged or macvlan NICs).

The IP filtering features block ARP and NDP advertisements that contain a spoofed IP, as well as blocking any
packets that contain a spoofed source address.

If `security.ipv4_filtering` or `security.ipv6_filtering` is enabled and the instance cannot be allocated an IP
address (because `ipvX.address=none` or there is no DHCP service enabled on the bridge) then all IP traffic for
that protocol is blocked from the instance.

When `security.ipv6_filtering` is enabled IPv6 router advertisements are blocked from the instance.

When `security.ipv4_filtering` or `security.ipv6_filtering` is enabled, any Ethernet frames that are not ARP,
IPv4 or IPv6 are dropped. This prevents stacked VLAN QinQ (802.1ad) frames from bypassing the IP filtering.

### Routed NIC security

An alternative networking mode is available called `routed` that provides a veth pair between container and host.
In this networking mode the LXD host functions as a router and static routes are added to the host directing
traffic for the container's IPs towards the container's veth interface.

By default the veth interface created on the host has its `accept_ra` setting disabled to prevent router
advertisements from the container modifying the IPv6 routing table on the LXD host. In addition to that the
`rp_filter` on the host is set to `1` to prevent source address spoofing for IPs that the host does not know the
container has.
