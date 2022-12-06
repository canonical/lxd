---
discourse: 8355
---

(devices-proxy)=
# Type: `proxy`

```{note}
The `proxy` device type is supported for both containers (NAT and non-NAT modes) and VMs (NAT mode only).
It supports hotplugging for both containers and VMs.
```

Proxy devices allow forwarding network connections between host and instance.
This method makes it possible to forward traffic hitting one of the host's addresses to an address inside the instance, or to do the reverse and have an address in the instance connect through the host.

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

To add a `proxy` device, use the following command:

    lxc config device add <instance_name> <device_name> proxy listen=<type>:<addr>:<port>[-<port>][,<port>] connect=<type>:<addr>:<port> bind=<host/instance_name>

## NAT mode

The proxy device also supports a NAT mode (`nat=true`), where packets are forwarded using NAT rather than being proxied through a separate connection.
This mode has the benefit that the client address is maintained without the need for the target destination to support the HAProxy PROXY protocol (which is the only way to pass the client address through when using the proxy device in non-NAT mode).

In NAT mode, the supported connection types are:

- `tcp <-> tcp`
- `udp <-> udp`

When configuring a proxy device with `nat=true`, you must ensure that the target instance has a static IP configured on its NIC device.

## Specifying IP addresses

Use the following command to configure a static IP for an instance NIC:

    lxc config device set <instance_name> <nic_name> ipv4.address=<ipv4_address> ipv6.address=<ipv6_address>

To define a static IPv6 address, the parent managed network must have `ipv6.dhcp.stateful` enabled.

When defining IPv6 addresses, use the square bracket notation, for example:

    connect=tcp:[2001:db8::1]:80

You can specify that the connect address should be the IP of the instance by setting the connect IP to the wildcard address (`0.0.0.0` for IPv4 and `[::]` for IPv6).

```{note}
The listen address can also use wildcard addresses when using non-NAT mode.
However, when using NAT mode, you must specify an IP address on the LXD host.
```

## Device options

`proxy` devices have the following device options:

Key             | Type      | Default       | Required  | Description
:--             | :--       | :--           | :--       | :--
`bind`          | string    | `host`        | no        | Which side to bind on (`host`/`instance`)
`connect`       | string    | -             | yes       | The address and port to connect to (`<type>:<addr>:<port>[-<port>][,<port>]`)
`gid`           | int       | `0`           | no        | GID of the owner of the listening Unix socket
`listen`        | string    | -             | yes       | The address and port to bind and listen (`<type>:<addr>:<port>[-<port>][,<port>]`)
`mode`          | int       | `0644`        | no        | Mode for the listening Unix socket
`nat`           | bool      | `false`       | no        | Whether to optimize proxying via NAT (requires that the instance NIC has a static IP address)
`proxy_protocol`| bool      | `false`       | no        | Whether to use the HAProxy PROXY protocol to transmit sender information
`security.gid`  | int       | `0`           | no        | What GID to drop privilege to
`security.uid`  | int       | `0`           | no        | What UID to drop privilege to
`uid`           | int       | `0`           | no        | UID of the owner of the listening Unix socket
