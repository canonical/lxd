---
discourse: 8355
---

(devices-proxy)=
# Type: `proxy`

Supported instance types: container (`nat` and non-`nat` modes), VM (`nat` mode only)

Proxy devices allow forwarding network connections between host and instance.
This makes it possible to forward traffic hitting one of the host's
addresses to an address inside the instance or to do the reverse and
have an address in the instance connect through the host.

The supported connection types are:

- `tcp <-> tcp`
- `udp <-> udp`
- `unix <-> unix`
- `tcp <-> unix`
- `unix <-> tcp`
- `udp <-> tcp`
- `tcp <-> udp`
- `udp <-> unix`
- `unix <-> udp`

The proxy device also supports a `nat` mode where packets are forwarded using NAT rather than being proxied through
a separate connection. This has benefit that the client address is maintained without the need for the target
destination to support the `PROXY` protocol (which is the only way to pass the client address through when using
the proxy device in non-NAT mode).

When configuring a proxy device with `nat=true`, you will need to ensure that the target instance has a static IP
configured in LXD on its NIC device. E.g.

```
lxc config device set <instance> <nic> ipv4.address=<ipv4.address> ipv6.address=<ipv6.address>
```

In order to define a static IPv6 address, the parent managed network needs to have `ipv6.dhcp.stateful` enabled.

In NAT mode the supported connection types are:

- `tcp <-> tcp`
- `udp <-> udp`

When defining IPv6 addresses use square bracket notation, e.g.

```
connect=tcp:[2001:db8::1]:80
```

You can specify that the connect address should be the IP of the instance by setting the connect IP to the wildcard
address (`0.0.0.0` for IPv4 and `[::]` for IPv6).

The listen address can also use wildcard addresses when using non-NAT mode. However when using `nat` mode you must
specify an IP address on the LXD host.

Key             | Type      | Default       | Required  | Description
:--             | :--       | :--           | :--       | :--
`listen`        | string    | -             | yes       | The address and port to bind and listen (`<type>:<addr>:<port>[-<port>][,<port>]`)
`connect`       | string    | -             | yes       | The address and port to connect to (`<type>:<addr>:<port>[-<port>][,<port>]`)
`bind`          | string    | `host`        | no        | Which side to bind on (`host`/`instance`)
`uid`           | int       | `0`           | no        | UID of the owner of the listening Unix socket
`gid`           | int       | `0`           | no        | GID of the owner of the listening Unix socket
`mode`          | int       | `0644`        | no        | Mode for the listening Unix socket
`nat`           | bool      | `false`       | no        | Whether to optimize proxying via NAT (requires instance NIC has static IP address)
`proxy_protocol`| bool      | `false`       | no        | Whether to use the HAProxy PROXY protocol to transmit sender information
`security.uid`  | int       | `0`           | no        | What UID to drop privilege to
`security.gid`  | int       | `0`           | no        | What GID to drop privilege to

```
lxc config device add <instance> <device-name> proxy listen=<type>:<addr>:<port>[-<port>][,<port>] connect=<type>:<addr>:<port> bind=<host/instance>
```
