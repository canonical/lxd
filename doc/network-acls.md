# Network ACL configuration

Network Access Control Lists (ACLs) define traffic rules that can then be applied to certain types of Instance NIC devices.
This provides the ability to control network access between different instances connected to the same network and
control access to and from other networks.

Network ACLs can either be applied directly to the desired NICs or can be applied to all NICs connected to a
network by assigning the ACL to the desired network.

The Instance NICs that have a particular ACL applied (either explicitly or implicitly from the network) make up a
logical group that can be referenced from other rules as a source or destination. This makes it possible to define
rules for groups of instances without needing to maintain IP lists or create additional subnets.

Once one or more ACLs are applied to a NIC (either explicitly or implicitly from the network) then a default reject
rule is added to the NIC, so if traffic doesn't match one of the rules in the applied ACLs then it is rejected.

This behaviour can be modified by using the network and NIC level `security.acls.default.ingress.action` and
`security.acls.default.egress.action` settings. The NIC level settings will override the network level settings.

Rules are defined for a particular direction (ingress or egress) in relation to the Instance NIC.
Ingress rules apply to traffic going towards the NIC, and egress rules apply to traffic leaving the NIC.

Rules are provided as lists, however the order of the rules in the list is not important and does not affect
filtering. See [Rule ordering and priorities](#rule-ordering-and-priorities).

Valid Network ACL names must:

- Be between 1 and 63 characters long
- Be made up exclusively of letters, numbers and dashes from the ASCII table
- Not start with a digit or a dash
- Not end with a dash

## Properties
The following are ACL properties:


Property         | Type       | Required | Description
:--              | :--        | :--      | :--
name             | string     | yes      | Unique name of Network ACL in Project
description      | string     | no       | Description of Network ACL
ingress          | rule list  | no       | Ingress traffic rules
egress           | rule list  | no       | Egress traffic rules
config           | string set | no       | Config key/value pairs (Only `user.*` custom keys supported)

ACL rules have the following properties:

Property          | Type       | Required | Description
:--               | :--        | :--      | :--
action            | string     | yes      | Action to take for matching traffic (`allow`, `reject` or `drop`)
state             | string     | yes      | State of rule (`enabled`, `disabled` or `logged`)
description       | string     | no       | Description of rule
source            | string     | no       | Comma separated list of CIDR or IP ranges, source subject name selectors (for ingress rules), or empty for any
destination       | string     | no       | Comma separated list of CIDR or IP ranges, destination subject name selectors (for egress rules), or empty for any
protocol          | string     | no       | Protocol to match (`icmp4`, `icmp6`, `tcp`, `udp`) or empty for any
source\_port      | string     | no       | If Protocol is `udp` or `tcp`, then comma separated list of ports or port ranges (start-end inclusive), or empty for any
destination\_port | string     | no       | If Protocol is `udp` or `tcp`, then comma separated list of ports or port ranges (start-end inclusive), or empty for any
icmp\_type        | string     | no       | If Protocol is `icmp4` or `icmp6`, then ICMP Type number, or empty for any
icmp\_code        | string     | no       | If Protocol is `icmp4` or `icmp6`, then ICMP Code number, or empty for any

## Rule ordering and priorities

Rules cannot be explicitly ordered. However LXD will order the rules based on the `action` property as follows:

 - `drop`
 - `reject`
 - `allow`
 - Automatic default action for any unmatched traffic (defaults to `reject`).

This means that multiple ACLs can be applied to a NIC without having to specify the combined rule ordering.
As soon as one of the rules in the ACLs matches then that action is taken and no other rules are considered.

The default reject action can be modified by using the network and NIC level `security.acls.default.ingress.action`
and `security.acls.default.egress.action` settings. The NIC level settings will override the network level settings.

## Subject name selectors

Subject name selectors can be used in the `source` field for ingress rules and in the `destination` field for
egress rules.

Instance NICs that are assigned a particular ACL (either directly or via the ACLs assigned to the network it is
connected to) make up a logical port group named after the ACL that can then be referenced as an ACL subject name
in other ACL rules using the format `<ACL_name>`.

E.g. `source=foo`

If the network supports [network peers](network-peers.md) then you can also reference traffic to/from the peer
connection by way of a network subject selector in the format `@<network_name>/<peer_name>`.

E.g. `source=@ovn1/mypeer`

When using a network subject selector, the network having the ACL applied to it must have the specified peer
connection or the ACL will refuse to be applied to it.

There are also two special network subject selectors called `@internal` and `@external` which represent network
local and external traffic respectively.

E.g. `source=@internal`

## Bridge limitations

Unlike OVN ACLs, `bridge` ACLs are applied *only* on the boundary between the bridge and the LXD host.
This means they can only be used to apply network policy for traffic going to/from external networks, and cannot be
used for intra-bridge firewalling (i.e for firewalling traffic between instances connected to the same bridge).

Additionally `bridge` ACLs do not support using subject name selectors.

When using the `iptables` firewall driver, you cannot use IP range subjects (e.g. `192.168.1.1-192.168.1.10`).

Baseline network service rules are added before ACL rules (in their respective INPUT/OUTPUT chains), because we
cannot differentiate between INPUT/OUTPUT and FORWARD traffic once we have jumped into the ACL chain. Because of
this ACL rules cannot be used to block baseline service rules.
