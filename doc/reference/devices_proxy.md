---
discourse: lxc:[Using&#32;proxy&#32;device&#32;to&#32;forward&#32;network&#32;connections&#32;from&#32;host&#32;to&#32;container&#32;in&#32;NAT&#32;mode](8355)
---

(devices-proxy)=
# Type: `proxy`

```{youtube} https://www.youtube.com/watch?v=IbAKwRBW8V0
:title: LXD proxy devices
```

```{note}
The `proxy` device type is supported for both containers (NAT and non-NAT modes) and VMs (NAT mode only).
It supports hotplugging for both containers and VMs.
```

Proxy devices allow you to forward network connections between a host and an instance running on that host.

You can use them to:

- Forward traffic from an address on the host to an address inside the instance.
- Do the reverse, enabling an address inside the instance to connect through the host.

In {ref}`devices-proxy-nat-mode`, proxy devices support TCP and UDP proxying (traffic forwarding).
In non-NAT mode, proxy devices can also forward traffic between Unix sockets, which is useful for tasks such as forwarding a GUI or audio traffic from a container to the host system. Additionally, they can proxy traffic across different protocols—for example, forwarding traffic from a TCP listener on the host to a Unix socket inside a container.

The supported connection types are:

- `tcp <-> tcp`
- `udp <-> udp`
- `unix <-> unix`
- `tcp <-> unix`
- `unix <-> tcp`
- `tcp <-> udp`
- `unix <-> udp`

To add a `proxy` device, use the following command:

    lxc config device add <instance_name> <device_name> proxy listen=<type>:<addr>:<port>[-<port>][,<port>] connect=<type>:<addr>:<port> bind=<host/instance_name>

```{tip}
Using a proxy device in NAT mode is very similar to adding a {ref}`network forward <network-forwards>`.

The difference is that network forwards are applied on a network level, while a proxy device is added for an instance.
In addition, network forwards cannot be used to proxy traffic between different connection types.
```

(devices-proxy-nat-mode)=
## NAT mode

The proxy device supports a NAT mode (`nat=true`), which forwards packets using NAT instead of creating a separate proxy connection.

This mode has the benefit that the client address is maintained without requiring the target destination to support the HAProxy PROXY protocol. This is necessary for passing client addresses in non-NAT mode.

However, NAT mode is only available when the host running the instance also acts as the gateway. This is the typical case when using `lxdbr0`, for example.

In NAT mode, the supported connection types are:

- `tcp <-> tcp`
- `udp <-> udp`

When configuring a proxy device with `nat=true`, you must ensure that the target instance has a static IP configured on its NIC device.

## Specifying IP addresses

Use the following command to configure a static IP for an instance NIC:

    lxc config device set <instance_name> <nic_name> ipv4.address=<ipv4_address> ipv6.address=<ipv6_address>

To define a static IPv6 address, the parent managed network must have `ipv6.dhcp.stateful` enabled.

When defining IPv6 addresses, use square bracket notation. Example:

    connect=tcp:[2001:db8::1]:80

You can specify that the connect address should be the IP of the instance by setting the connect IP to the wildcard address, which is `0.0.0.0` for IPv4 and `[::]` for IPv6.

```{note}
The listen address can also use wildcard addresses in non-NAT mode.
However, when using NAT mode, you must specify an IP address on the LXD host.
```

## Device options

`proxy` devices have the following device options:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group device-proxy-device-conf start -->
    :end-before: <!-- config group device-proxy-device-conf end -->
```

## Configuration examples

Add a `proxy` device that forwards traffic from one address (the `listen` address) to another address (the `connect` address) using NAT mode:

    lxc config device add <instance_name> <device_name> proxy nat=true listen=tcp:<ip_address>:<port> connect=tcp:<ip_address>:<port>

Add a `proxy` device that forwards traffic going to a specific IP to a Unix socket on an instance that might not have a network connection:

    lxc config device add <instance_name> <device_name> proxy listen=tcp:<ip_address>:<port> connect=unix:/<socket_path_on_instance>

Add a `proxy` device that forwards traffic going to a Unix socket on an instance that might not have a network connection to a specific IP address:

    lxc config device add <instance_name> <device_name> proxy bind=instance listen=unix:/<socket_path_on_instance> connect=tcp:<ip_address>:<port>

See {ref}`instances-configure-devices` for more information.
