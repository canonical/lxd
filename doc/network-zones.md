---
discourse: 12033,13128
---

# How to configure network zones

```{note}
Network zones are available for the {ref}`OVN network <network-ovn>` and the {ref}`Bridge network <network-bridge>`.
```

Network zones can be used to hold DNS records for LXD networks.

If you are operating a LXD cluster with multiple instances across a large set of networks, you can use network zones to automatically maintain valid forward and reverse records for all your instances.
Such DNS records are important not only for ease of access to the instances, but also to avoid hosted services getting flagged as potential spam due to lacking reverse DNS records.

Each network can be related to up to three zones for:

- Forward DNS records
- IPv4 reverse DNS records
- IPv6 reverse DNS records

LXD will then automatically manage forward and reverse records for all instances, network gateways and downstream network ports and serve those zones for zone transfer to the operator’s production DNS servers.

So for example, if you configure a zone for forward DNS records for `lxd.example.net` for your network, it is used to resolve DNS names for all instances in the network (`<instance_name>.lxd.example.net`).
If you configure a zone for IPv4 reverse DNS records for `30.192.10.in-addr.arpa` for a network using `10.192.30.0/24`, it is used for reverse DNS lookup for, for example, `10.192.30.100`.

## Enable the built-in DNS server

To make use of network zones, you must enable the built-in DNS server.

To do so, set the `core.dns_address` configuration option (see {doc}`server`) to the address where the DNS server should be available.

```{note}
The built-in DNS server supports only zone transfers through AXFR.
It cannot be directly queried for DNS records.
Therefore, the built-in DNS server must be used in combination with an external DNS server (bind9, nsd, ...), which will transfer the entire zone from LXD, refresh it upon expiry and provide authoritative answers to DNS requests.

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
lxc network zone create 30.192.10.in-addr.arpa
lxc network zone create 1.9.f.6.5.a.e.f.f.f.e.3.6.1.2.ip6.arpa
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
peers.NAME.address  | string     | no       | -       | IP address of a DNS server
peers.NAME.key      | string     | no       | -       | TSIG key for the server
dns.nameservers     | string set | no       | -       | Comma-separated list of DNS server FQDNs (for NS records)
network.nat         | bool       | no       | true    | Whether to generate records for NAT-ed subnets
user.*              | *          | no       | -       | User-provided free-form key/value pairs

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

A network zone automatically generates a DNS record for each instance.
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
name              | string     | yes      | Unique name of the record
description       | string     | no       | Description of the record
entries           | entry list | no       | A list of DNS entries
config            | string set | no       | Configuration options as key/value pairs (only `user.*` custom keys supported)

### Add or remove entries

To add an entry to the record, use the following command:

```bash
lxc network zone record entry add <network_zone> <record_name> <type> <value> [--ttl <TTL>]
```

This command adds a DNS entry with the specified type and value to the record.

For example:
```bash
lxc network zone record entry add <network_zone> <record_name> "A" "1.2.3.4"
lxc network zone record entry add <network_zone> <record_name> "AAAA" "1234::1234"
```
You can use the `--ttl` flag to set a custom time-to-live for the entry.
Otherwise, the default of 300 s is used.

You cannot edit an entry (except if you edit the full record with `lxc network zone record edit`), but you can delete entries with the following command:

```bash
lxc network zone record entry remove <network_zone> <record_name> <type> <value>
```
