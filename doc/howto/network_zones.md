---
discourse: 12033,13128
---

(network-zones)=
# How to configure network zones

```{note}
Network zones are available for the {ref}`network-ovn` and the {ref}`network-bridge`.
```

```{youtube} https://www.youtube.com/watch?v=2MqpJOogNVQ
```

Network zones can be used to serve DNS records for LXD networks.

You can use network zones to automatically maintain valid forward and reverse records for all your instances.
This can be useful if you are operating a LXD cluster with multiple instances across many networks.

Having DNS records for each instance makes it easier to access network services running on an instance.
It is also important when hosting, for example, an outbound SMTP service.
Without correct forward and reverse DNS entries for the instance, sent mail might be flagged as potential spam.

Each network can be related to up to three zones for:

- Forward DNS records
- IPv4 reverse DNS records
- IPv6 reverse DNS records

LXD will then automatically manage forward and reverse records for all instances, network gateways and downstream network ports and serve those zones for zone transfer to the operator’s production DNS servers.

## Generated records

For example, if you configure a zone for forward DNS records for `lxd.example.net` for your network, it generates records that resolve the following DNS names:

- For all instances in the network: `<instance_name>.lxd.example.net`
- For the network gateway: `<network_name>.gw.lxd.example.net`
- For downstream network ports (for network zones set on an uplink network with a downstream OVN network): `<project_name>-<downstream_network_name>.uplink.lxd.example.net`

You can check the records that are generated with your zone setup with the `dig` command.
For example, running `dig @<DNS_server_IP> -p 1053 axfr lxd.example.net` might give the following output:

```bash
lxd.example.net.              3600  IN	SOA	lxd.example.net. hostmaster.lxd.example.net. 1648118965 120 60 86400 30
default-my-ovn.uplink.lxd.example.net. 300 IN A 192.0.2.100
my-instance.lxd.example.net.  300   IN	A	192.0.2.76
my-uplink.gw.lxd.example.net. 300   IN	A	192.0.2.1
foo.lxd.example.net.          300	IN	A	8.8.8.8
lxd.example.net.              3600	IN	SOA	lxd.example.net. hostmaster.lxd.example.net. 1648118965 120 60 86400 30
```

If you configure a zone for IPv4 reverse DNS records for `2.0.192.in-addr.arpa` for a network using `192.0.2.0/24`, it generates reverse DNS records for, for example, `192.0.2.100`.

## Enable the built-in DNS server

To make use of network zones, you must enable the built-in DNS server.

To do so, set the `core.dns_address` configuration option (see {ref}`server`) to a local address on the LXD server.
This is the address on which the DNS server will listen.
Note that in a LXD cluster, the address may be different on each cluster member.

```{note}
The built-in DNS server supports only zone transfers through AXFR.
It cannot be directly queried for DNS records.
Therefore, the built-in DNS server must be used in combination with an external DNS server (`bind9`, `nsd`, ...), which will transfer the entire zone from LXD, refresh it upon expiry and provide authoritative answers to DNS requests.

Authentication for zone transfers is configured on a per-zone basis, with peers defined in the zone configuration and a combination of IP address matching and TSIG-key based authentication.
```

## Create and configure a network zone

Use the following command to create a network zone:

```bash
lxc network zone create <network_zone> [configuration_options...]
```

The following examples show how to configure a zone for forward DNS records, one for IPv4 reverse DNS records and one for IPv6 reverse DNS records, respectively:

```bash
lxc network zone create lxd.example.net
lxc network zone create 2.0.192.in-addr.arpa
lxc network zone create 1.0.0.0.1.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa
```

```{note}
Zones must be globally unique, even across projects.
If you get a creation error, it might be due to the zone already existing in another project.
```

You can either specify the configuration options when you create the network or configure them afterwards with the following command:

```bash
lxc network zone set <network_zone> <key>=<value>
```

Use the following command to edit a network zone in YAML format:

```bash
lxc network zone edit <network_zone>
```

### Configuration options

The following configuration options are available for network zones:

Key                 | Type       | Required | Default | Description
:--                 | :--        | :--      | -       | :--
`peers.NAME.address`| string     | no       | -       | IP address of a DNS server
`peers.NAME.key`    | string     | no       | -       | TSIG key for the server
`dns.nameservers`   | string set | no       | -       | Comma-separated list of DNS server FQDNs (for NS records)
`network.nat`       | bool       | no       | `true`  | Whether to generate records for NAT-ed subnets
`user.*`            | *          | no       | -       | User-provided free-form key/value pairs

## Add a network zone to a network

To add a zone to a network, set the corresponding configuration option in the network configuration:

- For forward DNS records: `dns.zone.forward`
- For IPv4 reverse DNS records: `dns.zone.reverse.ipv4`
- For IPv6 reverse DNS records: `dns.zone.reverse.ipv6`

For example:

```bash
lxc network set <network_name> dns.zone.forward="lxd.example.net"
```

Zones belong to projects and are tied to the `networks` features of projects.
You can restrict projects to specific domains and sub-domains through the `restricted.networks.zones` project configuration key.

## Add custom records

A network zone automatically generates forward and reverse records for all instances, network gateways and downstream network ports.
If required, you can manually add custom records to a zone.

To do so, use the `lxc network zone record` command.

### Create a record

Use the following command to create a record:

```bash
lxc network zone record create <network_zone> <record_name>
```

This command creates an empty record without entries and adds it to a network zone.

#### Record properties

Records have the following properties:

Property          | Type       | Required | Description
:--               | :--        | :--      | :--
`name`            | string     | yes      | Unique name of the record
`description`     | string     | no       | Description of the record
`entries`         | entry list | no       | A list of DNS entries
`config`          | string set | no       | Configuration options as key/value pairs (only `user.*` custom keys supported)

### Add or remove entries

To add an entry to the record, use the following command:

```bash
lxc network zone record entry add <network_zone> <record_name> <type> <value> [--ttl <TTL>]
```

This command adds a DNS entry with the specified type and value to the record.

For example, to create a dual-stack web server, add a record with two entries similar to the following:

```bash
lxc network zone record entry add <network_zone> <record_name> A 1.2.3.4
lxc network zone record entry add <network_zone> <record_name> AAAA 1234::1234
```

You can use the `--ttl` flag to set a custom time-to-live (in seconds) for the entry.
Otherwise, the default of 300 seconds is used.

You cannot edit an entry (except if you edit the full record with `lxc network zone record edit`), but you can delete entries with the following command:

```bash
lxc network zone record entry remove <network_zone> <record_name> <type> <value>
```
