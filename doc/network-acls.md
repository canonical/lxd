# Network ACL configuration

Network Access Control Lists (ACLs) define traffic rules that can then be applied to certain types of Instance NIC devices.
This provides the ability to control network access between different instances connected to the same network and
control access to and from the external network.

Network ACLs can either be applied directly to the desired NICs or can be applied to all NICs connected to a
network by assigning applying the ACL to the desired network.

The Instance NICs that have a particular ACL applied (either explicitly or implicitly from the network) make up a
logical group that can be referenced from other rules as a source or destination. This makes it possible to define
rules for groups of instances without needing to maintain IP lists or create additional subnets.

Network ACLs come with an implicit default rule (that defaults to `reject` unless `default.action` is set), so if
traffic doesn't match one of the defined rules in an ACL then all other traffic is dropped.

Rules are defined on for a particular direction (ingress or egress) in relation to the Instance NIC.
Ingress rules apply to traffic going towards the NIC, and egress rules apply to traffic leave the NIC.

Rules are provided as lists, however the order of the rules in the list is not important and does not affect filtering.

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
config           | string set | no       | Config key/value pairs (in addition to `user.*` custom keys, see below)

Config properties:

Property         | Type       | Required | Description
:--              | :--        | :--      | :--
default.action   | string     | no       | What action to take for traffic hitting the default rule (default `reject`)
default.logged   | boolean    | no       | Whether or not to log traffic hitting the default rule (default `false`)

ACL rules have the following properties:

Property          | Type       | Required | Description
:--               | :--        | :--      | :--
action            | string     | yes      | Action to take for matching traffic (`allow`, `reject` or `drop`)
state             | string     | yes      | State of rule (`enabled`, `disabled` or `logged`)
description       | string     | no       | Description of rule
source            | string     | no       | Comma separated list of CIDR or IP ranges, source ACL names or #external/#internal (for ingress rules), or empty for any
destination       | string     | no       | Comma separated list of CIDR or IP ranges, destination ACL names or #external/#internal (for egress rules), or empty for any
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
 - Automatic default rule action for any unmatched traffic (defaults to `reject` if `default.action` not specified).

 This means that multiple ACLs can be applied to a NIC without having to specify the combined rule ordering.
 As soon as one of the rules in the ACLs matches then that action is taken and no other rules are considered.

## Port group selectors

The Instance NICs that are assigned a particular ACL make up a logical port group that can then be referenced by
name in other ACL rules.

There are also two special selectors called `#internal` and `#external` which represent network local and external
traffic respectively.

Port group selectors can be used in the `source` field for ingress rules and in the `destination` field for egress rules.
