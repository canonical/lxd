# Network Zones configuration
Network zones are used to hold DNS records for LXD networks.

Each network can be related to up to 3 zones for:

 - Forward DNS records
 - IPv4 reverse DNS records
 - IPv6 reverse DNS records

This is controlled through `dns.zone.forward`, `dns.zone.reverse.ipv4`
and `dns.zone.reverse.ipv6` in network configuration. LXD will then be
automatically managing forward and reverse records for all instances,
network gateways and downstream network ports.

To enable the built-in DNS server, `core.dns_address` must be set in the
server configuration.

The built-in DNS server only supports zone transfers through AXFR, it
cannot be directly queried for DNS records. This means that this feature
expects the use of an external DNS server (bind9, nsd, ...) which will
transfer the entire zone from LXD, refresh it upon expiry and provide
authoritative answers to DNS requests.

Authentication for zone transfer is configured on a per-zone basis with
peers defined in zone configuration and a combination of IP address
matching and TSIG key based authentication.

Zones belong to projects and are tied to the `networks` features of projects.

Zone names must be globally unique, even across projects, so it's
possible to get a creation error due to a zone already existing in
another project.

It is possible to restrict projects to specific domains and sub-domains
through the `restricted.networks.zones` project configuration key.

## Properties
The following are network zone properties:

Property            | Type       | Required | Description
:--                 | :--        | :--      | :--
peers.NAME.address  | string     | no       | IP address of a DNS server
peers.NAME.key      | string     | no       | TSIG key for the server
dns.nameservers     | string set | no       | Comma separated list of DNS server FQDNs (for NS records)

Additionally the `user.` key namespace is also supported for user-provided free-form key/value.
