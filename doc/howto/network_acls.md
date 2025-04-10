---
discourse: lxc:[Network&#32;ACL&#32;logging](13223)
---

(network-acls)=
# How to configure network ACLs

```{note}
Network ACLs are available for the {ref}`OVN NIC type <nic-ovn>`, the {ref}`network-ovn` and the {ref}`network-bridge` (with some exceptions; see {ref}`network-acls-bridge-limitations`).
```

```{youtube} https://www.youtube.com/watch?v=mu34G0cX6Io
```

Network {abbr}`ACLs (Access Control Lists)` define rules for controlling traffic:

- Between instances connected to the same network
- To and from other networks

Network ACLs can be assigned directly to the {abbr}`NIC (Network Interface Controller)` of an instance, or to a network. When assigned to a network, the ACL applies indirectly to all NICs connected to that network.

When an ACL is assigned to multiple instance NICs, either directly or indirectly, those NICs form a logical port group. You can use the name of that ACL to refer to that group in the traffic rules of other ACLs. For more information, see: {ref}`network-acls-selectors-subject-name`.

(network-acls-list)=
## List ACLs

`````{tabs}
````{group-tab} CLI

To list all ACLs, run:

```bash
lxc network acl list
```

````
% End of group-tab CLI

````{group-tab} API

To list all ACLs, query the [`GET /1.0/network-acls`](swagger:/network-acls/network_acls_get) endpoint:

```bash
lxc query --request GET /1.0/network-acls
```

You can also use {ref}`recursion <rest-api-recursion>` to list the ACLs with a higher level of detail:

```bash
lxc query --request GET /1.0/network-acls?recursion=1
```

````
% End of group-tab API

`````

(network-acls-show)=
## Show an ACL

`````{tabs}
````{group-tab} CLI

To show details about a specific ACL, run:

```bash
lxc network acl show <ACL-name>
```

Example:

```bash
lxc network acl show my-acl
```

````
% End of group-tab CLI

````{group-tab} API

For details about a specific ACL, query the [`GET /1.0/network-acls/{ACL-name}`](swagger:/network-acls/network_acl_get) endpoint`:

```bash
lxc query --request GET /1.0/network-acls/{ACL-name}
```

Example:

```bash
lxc query --request GET /1.0/network-acls/my-acl
```

````
% End of group-tab API

`````

(network-acls-create)=
## Create an ACL

(network-acls-name-requirements)=
### Name requirements

Network ACL names must meet the following requirements:

- Must be between 1 and 63 characters long.
- Can contain only ASCII letters (a–z, A–Z), numbers (0–9), and dashes (-).
- Cannot begin with a digit or a dash.
- Cannot end with a dash.

### Instructions

`````{tabs}
````{group-tab} CLI

To create an ACL, run:

```bash
lxc network acl create <ACL-name> [user.KEY=value ...]
```

- You must provide an ACL name that meets the {ref}`network-acls-name-requirements`.
- You can optionally provide one or more custom `user` keys to store metadata or other information.

ACLs have no rules upon creation via command line, so as a next step, {ref}`add rules <network-acls-rules>` to the ACL. You can also {ref}`edit the ACL configuration <network-acls-edit>`, or {ref}`assign the ACL to a network or NIC <network-acls-assign>`.

Another way to create ACLs from the command line is to provide a YAML configuration file:

```bash
lxc network acl create <ACL-name> < <filename.yaml>
```

This file can include any other {ref}`network-acls-properties`, including the `egress` and `ingress` properties for defining {ref}`ACL rules <network-acls-rules>`. See the second example in the set below.

### Examples

Create an ACL with the name `my-acl` and an optional custom user key:

```bash
lxc network acl create my-acl user.my-key=my-value
```

Create an ACL using a YAML configuration file:

First, create a file named `config.yaml` with the following content:

```yaml
description: Allow web traffic from internal network
config:
  user.owner: devops
ingress:
  - action: allow
    description: Allow HTTP/HTTPS from internal
    protocol: tcp
    source: "@internal"
    destination_port: "80,443"
    state: enabled
```

Note that the custom user keys are stored under the `config` property.

The following command creates an ACL from that file's configuration:

```bash
lxc network acl create my-acl < config.yaml
```

````
% End of group-tab CLI

````{group-tab} API

To create an ACL, query the [`POST /1.0/network-acls`](swagger:/network-acls/network_acls_post) endpoint:

```bash
lxc query --request POST /1.0/network-acls --data '{
  "name": "<ACL-name>",
  "config": {
    "user.<custom-key-name>": "<custom-key-value>"
  },
  "description": "<description of the ACL>",
  "egress": [{<egress rule object>}, {<another egress rule object>, ...}],
  "ingress": [{<ingress rule object>}, {<another ingress rule object>, ...}]
}'
```

- You must provide an ACL name that meets the {ref}`network-acls-name-requirements`.
- You can optionally provide one or more custom `config.user.*` keys to store metadata or other information.
- The `ingress` and `egress` lists contain rules for inbound and outbound traffic. See {ref}`network-acls-rules` for details.

### Examples

Create an ACL with the name `my-acl`, a custom user key of `my-key`, and a `description`:

```bash
lxc query --request POST /1.0/network-acls --data '{
  "name": "my-acl",
  "config": {
    "user.my-key": "my-value"
  },
  "description": "Web servers"
}'
```

Create an ACL with the name `my-acl` and an `ingress` rule:

```bash
lxc query --request POST /1.0/network-acls --data '{
  "name": "my-acl",
  "ingress": [
    {
      "action": "drop",
      "state": "enabled"
    }
  ]
}'
```

````
% End of group-tab API

`````

(network-acls-properties)=
### ACL properties

ACLs have the following properties:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group network-acl-acl-properties start -->
    :end-before: <!-- config group network-acl-acl-properties end -->
```

(network-acls-rules)=
## ACL rules

Each ACL contains two lists of rules:

- Rules in the `egress` list apply to outbound traffic from the NIC.
- Rules in the `ingress` list apply to inbound traffic to the NIC.

For both `egress` and `ingress`, the rule configuration looks like this:

`````{tabs}
````{group-tab} YAML

```yaml
action: <allow|reject|drop>
description: <description>
destination: <destination-IP-range>
destination_port: <destination-port-number>
icmp_code: <ICMP-code>
icmp_type: <ICMP-type>
protocol: <icmp4|icmp6|tcp|udp>
source: <source-of-traffic>
source_port: <source-port-number>
state: <enabled|disabled|logged>
```

````
% End of group-tab YAML

````{group-tab} JSON

```
{
  "action": "<allow|reject|drop>",
  "description": "<description>",
  "destination": "<destination-IP-range>",
  "destination_port": "<destination-port-number>",
  "icmp_code": "<ICMP-code>",
  "icmp_type": "<ICMP-type>",
  "protocol": "<icmp4|icmp6|tcp|udp>",
  "source": "<source-of-traffic>",
  "source_port": "<source-port-number>",
  "state": "<enabled|disabled|logged>"
}
```

````
% End of group-tab JSON

`````

- The `action` property is required.
- The `state` property defaults to `"enabled"` if unset.
- The `source` and `destination` properties can be specified as one or more CIDR blocks, IP ranges, or {ref}`selectors <network-acls-selectors>`. If left empty, they match any source or destination. Comma-separate multiple values.
- If the `protocol` is unset, it matches any protocol.
- The `"destination_port"` and `"source_port"` properties and `"icmp_code"` and `"icmp_type"` properties are mutually exclusive sets. Although both sets are shown in the same rule above to demonstrate the syntax, they never appear together in practice.
   - The `"destination_port"` and `"source_port"` properties are only available when the `"protocol"` for the rule is `"tcp"` or `"udp"`.
   - The [`"icmp_code"`](https://www.iana.org/assignments/icmp-parameters/icmp-parameters.xhtml#icmp-parameters-codes) and [`"icmp_type"`](https://www.iana.org/assignments/icmp-parameters/icmp-parameters.xhtml#icmp-parameters-types) properties are only available when the `"protocol"` is `"icmp4"` or `"icmp6"`.
- The `"state"` is `"enabled"` by default. The `"logged"` value is used to {ref}`log traffic <network-acls-log>` to a rule.

For more information, see: {ref}`network-acls-rule-properties`.

### Add a rule

`````{tabs}
````{group-tab} CLI

To add a rule to an ACL, run:

```bash
lxc network acl rule add <ACL-name> <egress|ingress> [properties...]
```

#### Example

Add an `egress` rule with an `action` of `drop` to `my-acl`:

```bash
lxc network acl rule add my-acl egress action=drop
```

````
% End of group-tab CLI

````{group-tab} API

There is no specific endpoint for adding a rule. Instead, you must {ref}`edit the full ACL <network-acls-edit>`, which contains the `egress` and `ingress` lists.

````
% End of group-tab API

`````

```bash
lxc network acl rule remove <ACL_name> <direction> [properties...]
```

You must either specify all properties needed to uniquely identify a rule or add `--force` to the command to delete all matching rules.

### Rule ordering and priorities

Rules are provided as lists.
However, the order of the rules in the list is not important and does not affect
filtering.

LXD automatically orders the rules based on the `action` property as follows:

- `drop`
- `reject`
- `allow`
- Automatic default action for any unmatched traffic (defaults to `reject`, see {ref}`network-acls-defaults`).

This means that when you apply multiple ACLs to a NIC, there is no need to specify a combined rule ordering.
If one of the rules in the ACLs matches, the action for that rule is taken and no other rules are considered.

### Rule properties

ACL rules have the following properties:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group network-acl-rule-properties start -->
    :end-before: <!-- config group network-acl-rule-properties end -->
```

(network-acls-selectors)=
### Use selectors in rules

```{note}
This feature is supported only for the {ref}`OVN NIC type <nic-ovn>` and the {ref}`network-ovn`.
```

The `source` field (for ingress rules) and the `destination` field (for egress rules) support using selectors instead of CIDR or IP ranges.

With this method, you can use ACL groups or network selectors to define rules for groups of instances without needing to maintain IP lists or create additional subnets.

(network-acls-groups)=
#### ACL groups

Instance NICs that are assigned a particular ACL (either explicitly or implicitly through a network) make up a logical port group.

Such ACL groups are called *subject name selectors*, and they can be referenced with the name of the ACL in other ACL groups.

For example, if you have an ACL with the name `foo`, you can specify the group of instance NICs that are assigned this ACL as source with `source=foo`.

#### Network selectors

You can use *network subject selectors* to define rules based on the network that the traffic is coming from or going to.

There are two special network subject selectors called `@internal` and `@external`.
They represent network local and external traffic, respectively.
For example:

```bash
source=@internal
```

If your network supports [network peers](network_ovn_peers.md), you can reference traffic to or from the peer connection by using a network subject selector in the format `@<network_name>/<peer_name>`.
For example:

```bash
source=@ovn1/mypeer
```

When using a network subject selector, the network that has the ACL applied to it must have the specified peer connection.
Otherwise, the ACL cannot be applied to it.

### Log traffic

Generally, ACL rules are meant to control the network traffic between instances and networks.
However, you can also use them to log specific network traffic, which can be useful for monitoring, or to test rules before actually enabling them.

To add a rule for logging, create it with the `state=logged` property.
You can then display the log output for all logging rules in the ACL with the following command:

```bash
lxc network acl show-log <ACL_name>
```

(network-acls-edit)=
## Edit an ACL

Use the following command to edit an ACL:

```bash
lxc network acl edit <ACL_name>
```

This command opens the ACL in YAML format for editing.
You can edit both the ACL configuration and the rules.

## Assign an ACL

After configuring an ACL, you must assign it to a network or an instance NIC.

To do so, add it to the `security.acls` list of the network or NIC configuration.
For networks, use the following command:

```bash
lxc network set <network_name> security.acls="<ACL_name>"
```

For instance NICs, use the following command:

```bash
lxc config device set <instance_name> <device_name> security.acls="<ACL_name>"
```

(network-acls-defaults)=
## Configure default actions

When one or more ACLs are applied to a NIC (either explicitly or implicitly through a network), a default reject rule is added to the NIC.
This rule rejects all traffic that doesn't match any of the rules in the applied ACLs.

You can change this behavior with the network and NIC level `security.acls.default.ingress.action` and `security.acls.default.egress.action` settings.
The NIC level settings override the network level settings.

For example, to set the default action for inbound traffic to `allow` for all instances connected to a network, use the following command:

```bash
lxc network set <network_name> security.acls.default.ingress.action=allow
```

To configure the same default action for an instance NIC, use the following command:

```bash
lxc config device set <instance_name> <device_name> security.acls.default.ingress.action=allow
```

(network-acls-bridge-limitations)=
## Bridge limitations

When using network ACLs with a bridge network, be aware of the following limitations:

- Unlike OVN ACLs, bridge ACLs are applied only on the boundary between the bridge and the LXD host.
  This means they can only be used to apply network policies for traffic going to or from external networks.
  They cannot be used for to create {spellexception}`intra-bridge` firewalls, thus firewalls that control traffic between instances connected to the same bridge.
- {ref}`ACL groups and network selectors <network-acls-selectors>` are not supported.
- When using the `iptables` firewall driver, you cannot use IP range subjects (for example, `192.0.2.1-192.0.2.10`).
- Baseline network service rules are added before ACL rules (in their respective INPUT/OUTPUT chains), because we cannot differentiate between INPUT/OUTPUT and FORWARD traffic once we have jumped into the ACL chain.
  Because of this, ACL rules cannot be used to block baseline service rules.
