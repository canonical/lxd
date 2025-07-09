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

````{group-tab} UI

View ACL information from the {guilabel}`Networking` section of the main navigation.
````
% End of group-tab UI

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

````{group-tab} UI

To show the detail page of an ACL, select the desired ACL from the {guilabel}`ACLs` page.

```{figure} /images/networks/network_ACLs.png
:width: 80%
:alt: A Network ACL in LXD
```

````
% End of group-tab UI

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

````{group-tab} UI

To create an ACL, navigate to {guilabel}`ACLs` from the {guilabel}`Networking` tab in the main navigation, then click the {guilabel}`Create ACL` button in the upper-right corner.

```{figure} /images/networks/network_ACL_create.png
:width: 80%
:alt: Create an ACL in LXD
```

````
% End of group-tab UI

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

````{group-tab} UI

To add an ingress or egress rule to an ACL, go to its {ref}`detail page <network-acls-show>`.

Click {guilabel}`Add rule`, then configure your ingress or egress settings.

```{figure} /images/networks/network_ACL_addrule.png
:width: 80%
:alt: Add a rule to an ACL in LXD
```

Note that the {guilabel}`Save changes` button displays the number of changes you have made. Save your changes.

````
% End of group-tab UI

`````

### Remove a rule

`````{tabs}
````{group-tab} CLI

To remove a rule from an ACL, run:

```bash
lxc network acl rule remove <ACL-name> <egress|ingress> [properties...]
```

You must either specify all properties needed to uniquely identify a rule or add `--force` to the command to delete all matching rules.

````
% End of group-tab CLI

````{group-tab} API

There is no specific endpoint for removing a rule. Instead, you must {ref}`edit the full ACL <network-acls-edit>`, which contains the `egress` and `ingress` lists.

````
% End of group-tab API

````{group-tab} UI

To remove a rule from an ACL, go to the ACL's {ref}`detail page <network-acls-show>`. From the row of the rule to remove, click the {guilabel}`Delete` button.

```{figure} /images/networks/network_ACL_remove_edit.png
:width: 80%
:alt: Add a rule to an ACL in LXD
```

Note that the {guilabel}`Save changes` button displays the number of changes you have made. Save your changes.

````
% End of group-tab UI

`````

### Edit a rule

You cannot edit a rule directly. Instead, you must {ref}`edit the full ACL <network-acls-edit>`, which contains the `egress` and `ingress` lists.

### Rule ordering and application of actions

ACL rules are defined as lists, but their order within the list does not affect how they are applied.

LXD automatically prioritizes rules based on the action property, in the following order:

- `drop`
- `reject`
- `allow`
- The default action for unmatched traffic (defaults to `reject`, see {ref}`network-acls-defaults`)

When you assign multiple ACLs to a NIC, you do not need to coordinate rule order across them. As soon as a rule matches, its action is applied and no further rules are evaluated.

(network-acls-rule-properties)=
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

In ACL rules, the `source` and `destination` properties support using selectors instead of CIDR blocks or IP ranges. You can only use selectors in the `source` of `ingress` rules, and in the `destination` of `egress` rules.

Using selectors allows you to define rules for groups of instances instead of managing lists of IP addresses or subnets manually.

There are two types of selectors:

- subject name selectors (ACL groups)
- network subject selectors

(network-acls-selectors-subject-name)=
#### Subject name selectors (ACL groups)

When an ACL is assigned to multiple instance NICs, either directly or through their networks, those NICs form a logical port group. You can use the name of that ACL as a _subject name selector_ to refer to that group in the egress and ingress lists of other ACLs.

For example, if you have an ACL with the name `my-acl`, you can specify the group of instance NICs that are assigned this ACL as an egress or ingress rule's source by setting `source` to `my-acl`.

(network-acls-selectors-network-subject)=
#### Network subject selectors

Use _network subject selectors_ to define rules based on the network that the traffic is coming from or going to.

All network subject selectors begin with the `@` symbol. There are two special network subject selectors called `@internal` and `@external`. They represent the network's local and external traffic, respectively.

Here's an example ACL rule (in YAML) that allows all internal traffic with the specified destination port:

```yaml
ingress:
  - action: allow
    description: Allow HTTP/HTTPS from internal
    protocol: tcp
    source: "@internal"
    destination_port: "80,443"
    state: enabled
```

If your network supports [network peers](network_ovn_peers.md), you can reference traffic to or from the peer connection by using a network subject selector in the format `@<network-name>/<peer-name>`. Example:

```yaml
source: "@my-network/my-peer"
```

When using a network subject selector, the network that has the ACL assigned to it must have the specified peer connection.

(network-acls-log)=
### Log traffic

ACL rules are primarily used to control network traffic between instances and networks. However, they can also be used to log specific types of traffic, which is useful for monitoring or testing rules before enabling them.

To configure a rule so that it only logs traffic, configure its `state` to `logged` when you {ref}`add the rule <network-acls-rules>` or {ref}`edit the ACL <network-acls-edit>`.

#### View logs

`````{tabs}
````{group-tab} CLI

To display the logs for all `logged` rules in an ACL, run:

```bash
lxc network acl show-log <ACL-name>
```

````
% End of group-tab CLI

````{group-tab} API

To display the logs for all `logged` rules in an ACL, query the [`GET /1.0/network-acls/{ACL-name}/log`](swagger:/network-acls/network_acl_log_get) endpoint:

```bash
lxc query --request GET /1.0/network-acls/{ACL-name}/log
```

##### Example

```bash
lxc query --request GET /1.0/network-acls/my-acl/log
```

````
% End of group-tab API

````{group-tab} UI

Download a `.log` file of your ACL's logs from its {ref}`detail page <network-acls-show>` by clicking the {guilabel}`Download logs` button in the upper-right corner.
````
% End of group-tab UI

`````

```{note}
If your attempt to view logs returns no data, that means either:
- No `logged` rules have matched any traffic yet.
- The ACL does not contain any rules with a `state` of `logged`.

When displaying logs for an ACL, LXD intentionally displays all existing logs for that ACL, including logs from formerly `logged` rules that are no longer set to log traffic. Thus, if you see logs from an ACL rule, that does not necessarily mean that its `state` is _currently_ set to `logged`.
```

(network-acls-edit)=
## Edit an ACL

(network-acls-edit-rename)=
### Rename an ACL

Requirements:

- You can only rename an ACL that is not currently {ref}`assigned to a NIC or network <network-acls-assign>`.
- The new name must meet the {ref}`network-acls-name-requirements`.

`````{tabs}
````{group-tab} CLI

To rename an ACL, run:

```bash
lxc network acl rename <old-ACL-name> <new-ACL-name>
```

````
% End of group-tab CLI

````{group-tab} API

To rename an ACL, query the [`POST /1.0/network-acls/{ACL-name}`](swagger:/network-acls/network_acl_post) endpoint:

```bash
lxc query --request POST /1.0/network-acls/{ACL-name} --data '{
  "name": "<new-ACL-name>"
}'
```

#### Example

Rename an ACL named `web-traffic` to `internal-web-traffic`:

```bash
lxc query --request POST /1.0/network-acls/web-traffic --data '{
  "name": "internal-web-traffic"
}'
```

````
% End of group-tab API

````{group-tab} UI

To rename an ACL, go to its {ref}`detail page <network-acls-show>` and select its name in the header.
````
% End of group-tab UI

`````

(network-acls-edit-properties)=
### Edit other properties

`````{tabs}
````{group-tab} CLI

Run:

```bash
lxc network acl edit <ACL-name>
```

This command opens the ACL configuration in YAML format for editing. You can edit any part of the configuration _except_ for the ACL name, including the custom user keys.

````
% End of group-tab CLI

````{group-tab} API

You can update any ACL property except for `name`, including the custom user keys, by querying the [`PUT /1.0/network-acls/{ACL-name}`](swagger:/network-acls/network_acl_put) endpoint:

```bash
lxc query --request PUT /1.0/network-acls/{ACL-name} --data '{
  "config": {
    "user.<custom key name>": "<custom key value>"
  },
  "description": "<description of the ACL>",
  "egress": [<egress rule>, <another egress rule...>,...],
  "ingress": [<ingress rule>, <another ingress rule...>,...]
}'
```

```{caution}
Any properties you omit from this request (aside from the ACL `name`) will be reset to defaults. See: {ref}`rest-api-put`.
```

If you _only_ want to update the `config` custom user keys, see: {ref}`network-acls-edit-custom-api`.

#### Example

Consider an ACL named `my-acl` with the following properties (shown in JSON):

```json
{
  "name": "my-acl",
  "config": {
    "user.my-key": "my-value"
  },
  "description": "My test ACL",
  "egress": [
    {
      "action": "allow",
      "state": "logged"
    }
  ]
  "ingress": [
    {
      "action": "drop",
      "state": "enabled"
    }
  ]
}
```

This query updates that ACL's `egress` rule `state` from `logged` to `enabled`:

```bash
lxc query --request PUT /1.0/network-acls/my-acl --data '{
  "egress": [
    {
      "action": "allow",
      "state": "enabled"
    }
  ]
}'
```

After the above query is run, `my-acl` contains the following properties:

```json
{
  "name": "test",
  "config": {},
  "description": "",
  "egress": [
    {
      "action": "allow",
      "state": "enabled"
    }
  ],
  "ingress": []
}
```

Note that the `description` and `ingress` properties have been reset to defaults because they were not provided in the API request.

To avoid this behavior and preserve the values of any existing properties, you must include them in the `PUT` request along with the updated property:

```bash
lxc query --request PUT /1.0/network-acls/my-acl --data '{
  "description": "My test ACL",
  "egress": [
    {
      "action": "allow",
      "state": "enabled"
    }
  ],
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

````{group-tab} UI

To edit an ACL, navigate to its {ref}`detail page <network-acls-show>`. From here, you can add or remove ingress or egress rules, as well as configure other settings.


````
% End of group-tab UI

`````

(network-acls-edit-custom-api)=
### Edit a custom user key via PATCH API

There's one more way to add or update a custom `config.user.*` key when using the API. Instead of the PUT method shown in the {ref}`network-acls-edit-properties` section above, you can query the [`PATCH /1.0/network-acls/{ACL-name}`](swagger:/network-acls/network_acl_patch) endpoint:

```bash
lxc query --request PATCH /1.0/network-acls/{ACL-name} --data '{
  "config": {
    "user.<custom-key-name>": "<custom-key-value>"
  }
}'
```

```{caution}
Any ACL properties you omit from this request (aside from `config` and `name`) will be reset to defaults.
```

This `PATCH` endpoint allows you to add or update custom `config.user.*` keys without affecting other existing `config.user.*` entries. However, this {ref}`partial update behavior <rest-api-patch>` applies _only_ to the `config` property. For the `description`, `egress`, and `ingress` properties, this request behaves like a {ref}`PUT request <rest-api-put>`: it replaces any provided values and resets any omitted properties to their defaults. Thus, ensure you include any properties you want to keep.

#### Example

Consider an ACL named `my-acl` with the following properties (shown in JSON):

```json
{
  "name": "my-acl",
  "description": "My test ACL",
  "config": {
    "user.my-key1": "1"
  },
}
```

The following query adds a `config.user.my-key2` key with the value of `2`:

```bash
lxc query --request PATCH /1.0/network-acls/my-acl --data '{
  "config": {
    "user.my-key2": "2"
  }
}'
```

After sending the above request, `my-acl`'s properties are updated to:

```json
{
  "name": "my-acl",
  "description": "",
  "config": {
    "user.my-key1": "1",
    "user.my-key2": "2"
  }
}
```

Note that the request _inserted_ the new `user.my-key2` key without affecting the pre-existing `user.my-key1` key. Also notice that the `description` property was not sent in the request, and thus was reset to an empty value.

(network-acls-delete)=
## Delete an ACL

You can only delete an ACL that is not {ref}`assigned to a NIC or network <network-acls-assign>`.

`````{tabs}
````{group-tab} CLI

To delete an ACL, run:

```bash
lxc network acl delete <ACL-name>
```

````
% End of group-tab CLI

````{group-tab} API

To delete an ACL, query the [`DELETE /1.0/network-acls/{ACL-name}`](swagger:/network-acls/network_acl_delete) endpoint:

```bash
lxc query --request DELETE /1.0/network-acls/{ACL-name}
```

````
% End of group-tab API

````{group-tab} UI

To delete an ACL, ensure that it is not assigned to an NIC or network. You can then delete it from its {ref}`detail page <network-acls-show>`.
````
% End of group-tab UI

`````

(network-acls-assign)=
## Assign an ACL

An ACL is inactive until it is assigned to one of the following targets:

- a {ref}`network-ovn`
- a {ref}`network-bridge`
- an {ref}`OVN NIC type of an instance <nic-ovn>`

To assign an ACL, you must update the `security.acls` option within its target's configuration.

Assigning one or more ACLs to a NIC or network adds a default rule that rejects all unmatched traffic. See {ref}`network-acls-defaults` for details.

### Assign an ACL to a bridge or OVN network

`````{tabs}
````{group-tab} CLI

To set the network's `security.acls`, run the following command. Set the value to a string that contains the ACL name or names you want to add, and comma-separate multiple names:

Set the network's `security.acls` to a string that contains the ACL name or names you want to add. Comma-separate multiple names:

```bash
lxc network set <network-name> security.acls="<ACL-name>[,<ACL-name>,...]"
```

For more information about using `lxc network set`, see: {ref}`network-configure`.

#### Example

Set the `my-network` network's `security.acls` to contain three ACLs:

```bash
lxc network set my-network security.acls="my-acl1,my-acl2,my-acl3"
```

````
% End of group-tab CLI

````{group-tab} API

To set the network's `security.acls`, query the [`PATCH /1.0/networks/{network-name}`](swagger:/networks/network_patch) endpoint. Set the value to a string that contains the ACL name or names you want to add, and comma-separate multiple names:

```bash
lxc query --request PATCH /1.0/networks/{network-name} --data '{
  "config": {
    "security.acls": "<ACL-name>[,<ACL-name>,...]"
  }
}'
```

#### Example

Set the `my-network` network's `security.acls` to contain three ACLs:

```bash
lxc query --request PATCH /1.0/networks/my-network --data '{
  "config": {
    "security.acls": "my-acl1,my-acl2,my-acl3"
  }
}'
```

````
% End of group-tab API

````{group-tab} UI

You can assign an ACL to a bridge or OVN network when {ref}`creating <network-create>` or {ref}`editing <network-configure>` the network. In either case, select your pre-configured ACL from the {guilabel}`ACLs` dropdown.

```{figure} /images/networks/network_create.png
:width: 80%
:alt: Create a network in LXD
```

````
% End of group-tab UI

`````

### Assign an ACL to the OVN NIC of an instance

For {abbr}`NICs (Network Interface Cards)`, ACLs can only be used with the {ref}`OVN NIC type <nic-ovn>`.

An NIC is considered a type of instance {ref}`device <devices>`. For general information about configuring instance devices, see: {ref}`instances-configure-devices`.

`````{tabs}
````{group-tab} CLI

To assign an ACL to an instance's OVN NIC, run:

```bash
lxc config device set <instance-name> <NIC-name> security.acls="<ACL-name>[,ACL-name,...]"
```

#### Example

Assign three ACLs to an instance's OVN NIC:

```bash
lxc config device set my-instance my-ovn-nic security.acls="my-acl1,my-acl2,my-acl3"
```

````
% End of group-tab CLI

````{group-tab} API

To assign an ACL to an instance's OVN NIC, query the [`PATCH /1.0/instances/{instance-name}`](swagger:/instances/instance_patch) endpoint. Set `security.acls` to a string that contains the ACL name or names you want to add, and comma-separate multiple names:

```bash
lxc query --request PATCH /1.0/instances/{instance-name} --data '{
  "devices": {
    "<NIC-name>": {
      "network": <network-name>,
      "type": "nic",
      "security.acls": "<ACL-name>[,<ACL-name>,...]",
      <other options>
    }
  }
}'
```

The `type` and `network` options are required in the body (see: {ref}`instances-configure-devices-api-required`).

```{caution}
Patching an instance device's configuration unsets any options for that device omitted from the PATCH request body. For more information, see {ref}`instances-configure-devices-api-patch-effects`.
```

##### Example

For `my-instance`, set its `my-ovn-nic` device's `security.acls` to contain three ACLs:

```bash
lxc query --request PATCH /1.0/instances/my-instance --data '{
  "devices": {
    "my-ovn-nic": {
      "network": "my-ovn-network",
      "type": "nic",
      "security.acls": "my-acl1,my-acl2,my-acl3"
    }
  }
}'
```

````
% End of group-tab API

`````

(network-acls-assign-additional)=
### Additional options

To view additional options for the `security.acls` lists, refer to the configuration options for the target network or NIC:

- Bridget network's {config:option}`network-bridge-network-conf:security.acls`
- OVN network's {config:option}`network-ovn-network-conf:security.acls`
- Instance's OVN NIC {config:option}`device-nic-ovn-device-conf:security.acls`

(network-acls-defaults)=
## Configure default actions

When one or more ACLs are assigned to a NIC—either directly or through its network—a default reject rule is added to the NIC.
This rule rejects all traffic that doesn't match any of the rules in the assigned ACLs.

You can change this behavior with the network- and NIC-level `security.acls.default.ingress.action` and `security.acls.default.egress.action` settings. The NIC-level settings override the network-level settings.

`````{tabs}
````{group-tab} CLI

### Configure a default action for a network

To set the default action for a network's egress or ingress traffic, run:

```bash
lxc network set <network-name> security.acls.default.<egress|ingress>.action=<allow|reject|drop>
```

#### Example

To set the default action for inbound traffic to `allow` for all instances on the `my-network` network, run:

```bash
lxc network set my-network security.acls.default.ingress.action=allow
```

### Configure a default action for an instance OVN NIC device

To set the default action for an instance OVN NIC's egress or ingress traffic, run:

```bash
lxc config device set <instance-name> <NIC-name> security.acls.default.<egress|ingress>.action=<allow|reject|drop>
```

#### Example

To set the default action for inbound traffic to `allow` for the `my-ovn-nic` device of `my-instance`, run:

```bash
lxc config device set my-instance my-ovn-nic security.acls.default.ingress.action=allow
```

````
% End of group-tab CLI

````{group-tab} API

### Configure a default action for a network

To set the default action for a network's egress or ingress traffic, query the [`PATCH /1.0/networks/{network-name}`](swagger:/networks/network_patch) endpoint:

```bash
lxc query --request PATCH /1.0/networks/{network-name} --data '{
  "config": {
    "security.acls.default.egress.action": "<allow|reject|drop>",
    "security.acls.default.ingress.action": "<allow|reject|drop>",
  }
}'
```

#### Example

Set the `my-network` network's default egress action to `allow`:

```bash
lxc query --request PATCH /1.0/networks/my-network --data '{
  "config": {
    "security.acls.default.egress.action": "allow"
  }
}'
```

### Configure a default action for an instance's OVN NIC device

To set the default action for an instance's OVN NIC's traffic, query the [`PATCH /1.0/instances/{instance-name}`](swagger:/instances/instance_patch) endpoint:

```bash
lxc query --request PATCH /1.0/instances/{instance-name} --data '{
  "devices": {
    "<NIC-name>": {
      "network": <network-name>,
      "type": "nic",
      "security.acls.default.<egress|ingress>.action": "<allow|reject|drop>"
      <other-options>
    }
  }
}'
```

The `type` and `network` options are required in the body (see: {ref}`instances-configure-devices-api-required`).

```{caution}
Patching an instance device's configuration unsets any options for that device omitted from the PATCH request body. For more information, see {ref}`instances-configure-devices-api-patch-effects`.
```

#### Example

This request sets the default action for inbound traffic to `allow` for the `my-ovn-nic` device of `my-instance`:

```bash
lxc query --request PATCH /1.0/instances/my-instance --data '{
  "devices": {
    "my-ovn-nic": {
      "network": "my-network",
      "type": "nic",
      "security.acls.default.ingress.action": "allow"
    }
  }
}'
```

````
% End of group-tab API

`````

(network-acls-bridge-limitations)=
## Bridge limitations

When using network ACLs with a bridge network, be aware of the following limitations:

- Unlike OVN ACLs, bridge ACLs apply only at the boundary between the bridge and the LXD host. This means they can enforce network policies only for traffic entering or leaving the host. {spellexception}`Intra-bridge` firewalls (rules controlling traffic between instances on the same bridge) are not supported.
- {ref}`ACL groups and network selectors <network-acls-selectors>` are not supported.
- If you're using the `iptables` firewall driver, you cannot use IP range subjects (such as `192.0.2.1-192.0.2.10`).
- Baseline network service rules are added before ACL rules in their respective INPUT/OUTPUT chains. Because we cannot differentiate between INPUT/OUTPUT and FORWARD traffic after jumping into the ACL chain, ACL rules cannot block these baseline rules.
