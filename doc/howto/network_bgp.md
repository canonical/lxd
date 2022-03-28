# How to configure LXD as a BGP server

LXD can act as a BGP server, effectively allowing to establish sessions with upstream BGP routers and announce the addresses and subnets that it's using.

This can be used to allow a LXD server or cluster to directly use internal/external address space, getting the specific subnets or addresses routed to the correct host for it to forward onto the target instance.

For this to work, `core.bgp_address`, `core.bgp_asn` and `core.bgp_routerid` must be set.
Once those are set, LXD will start listening for BGP sessions.

Peers can be defined on both `bridged` and `physical` managed networks. Additionally in the `bridged` case, a set of per-server configuration keys are also available to override the next-hop. When those aren't specified, the next-hop defaults to the address used for the BGP session.

The `physical` network case is used for `ovn` networks where the uplink network is the one holding the list of allowed subnets and the BGP configuration. Once that parent network is configured, children OVN networks will get their external subnets and addresses announced over BGP with the next-hop set to the OVN router address for the network in question.

The addresses and networks currently being advertised are:
 - Network `ipv4.address` or `ipv6.address` subnets when the matching `nat` property isn't set to `true`
 - Network `ipv4.nat.address` and `ipv6.nat.address` when those are set
 - Instance NIC routes defined through `ipv4.routes.external` or `ipv6.routes.external`

At this time, there isn't a way to only announce some specific routes/addresses to particular peers. Instead it's currently recommended to filter prefixes on the upstream routers.
