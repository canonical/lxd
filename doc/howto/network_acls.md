---
discourse: lxc:[Network&#32;ACL&#32;logging](13223)
---

(network-acls)=
# How to configure network ACLs

```{note}
Network ACLs are available for the {ref}`OVN NIC type <nic-ovn>`, the {ref}`network-ovn` and the {ref}`network-bridge` (with some exceptions, see {ref}`network-acls-bridge-limitations`).
```

```{youtube} https://www.youtube.com/watch?v=mu34G0cX6Io
```

Network {abbr}`ACLs (Access Control Lists)` define traffic rules that allow controlling network access between different instances connected to the same network, and access to and from other networks.

Network ACLs can be assigned directly to the {abbr}`NIC (Network Interface Controller)` of an instance or to a network.
When assigned to a network, the ACL applies to all NICs connected to the network.

Instance NICs that are assigned a particular ACL—either directly or through its network—form a logical port group. The name of that ACL can be used within the rules of other ACLs as a *subject name selector*.
See {ref}`network-acls-selectors-subject-name` for more information.

## List ACLs

`````{tabs}
````{group-tab} CLI

To list all ACLs, run:

```bash
lxc network acl list
```

````

````{group-tab} API

To list all ACLs, query the `/1.0/network-acls` endpoint:

```bash
lxc query --request GET /1.0/network-acls
```

See [the API reference](swagger:/network-acls/network_acls_get) for more information.

You can also use {ref}`recursion <rest-api-recursion>` to list the ACLs with a higher level of detail:

```bash
lxc query --request GET /1.0/network-acls?recursion=1
```

````
`````

(network-acls-show)=
## Show an ACL

`````{tabs}
````{group-tab} CLI

To show details about a specific ACL, run:

```bash
lxc network acl show <acl_name>
```

Example:

```bash
lxc network acl show my-acl
```

````

````{group-tab} API

For details about a specific ACL, query the following endpoint:

```bash
lxc query --request GET /1.0/network-acls/{name}
```

See [the API reference](swagger:/network-acls/network_acl_get) for more information.

Example:

```bash
lxc query --request GET /1.0/network-acls/my-acl
```

````
`````

(network-acls-create)=
## Create an ACL

Network ACL names must meet the following requirements:

- Must be between 1 and 63 characters long.
- Can contain only ASCII letters (a–z, A–Z), numbers (0–9), and dashes (-).
- Cannot begin with a digit or a dash.
- Cannot end with a dash.

`````{tabs}
````{group-tab} CLI

To create an ACL, run:

```bash
lxc network acl create <ACL_name> [user.KEY=value ...]
```

- You must provide an ACL name that meets the requirements described above.
- You can optionally provide one or more custom `user` keys to store metadata or other information.

ACLs have no rules upon creation via command line, so as a next step, {ref}`add rules <network-acls-rules>` to the ACL. You can also {ref}`edit the ACL configuration <network-acls-edit>`, or {ref}`assign the ACL to a network or NIC <network-acls-assign>`.

Another way to create ACLs from the command line is to provide a YAML configuration file:

```bash
lxc network acl create <ACL_NAME> < <filename.yaml>
```

This file must contain at least the ACL `name`, and it can include any other {ref}`network-acls-properties`, including the `egress` and `ingress` properties for defining {ref}`ACL rules <network-acls-rules>`. See the final example in the set below.

### Examples

Create an ACL with the name `acl1`:

```bash
lxc network acl create acl1
```

Create an ACL with the name `acl2` and a custom user key of `environment`:

```bash
lxc network acl create acl2 user.environment=dev
```

Create an ACL with the name `acl3`, and custom user keys of `environment` and `level`:

```bash
lxc network acl create acl3 user.environment=dev user.level=5
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
lxc network acl create example-web-acl < config.yaml
```

````

````{group-tab} API

To create an ACL, query the following endpoint:

```bash
lxc query --request POST /1.0/network-acls --data '{
  "name": "<ACL name>", # required
  "config": {
    "user.<custom key name>": "<custom key value>"
  },
  "description": "<description of the ACL>",
  "egress": [<egress rule>, <another egress rule...>,...],
  "ingress": [<ingress rule>, <another ingress rule...>,...]
}'
```

- You must provide an ACL `name` that meets the requirements described above.
- You can optionally provide one or more custom `config.user.*` keys to store metadata or other information.
- The `ingress` and `egress` lists contain rules for inbound and outbound traffic. See {ref}`network-acls-rules` for details.

See [the API reference](swagger:/network-acls/network_acls_post) for more information.

### Examples

Create an ACL with the name `acl1`:

```bash
lxc query --request POST /1.0/network-acls --data '{
  "name": "acl1"
}'
```

Create an ACL with the name `acl2` and a custom user key of `environment`:

```bash
lxc query --request POST /1.0/network-acls --data '{
  "name": "acl2",
  "config": {
    "user.environment": "dev"
  }
}'
```

Create an ACL with the name `acl3`, a custom user key of `environment`, and a `description`:

```bash
lxc query --request POST /1.0/network-acls --data '{
  "name": "acl3",
  "config": {
    "user.environment": "dev"
  },
  "description": "Web servers"
}'
```

Create an ACL with the name `acl4` and an `ingress` rule:

```bash
lxc query --request POST /1.0/network-acls --data '{
  "name": "acl4",
  "ingress": [
    {
      "action": "drop",
      "state": "enabled"
    }
  ]
}'
```

````

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
destination: <destination IP range>
destination_port: <destination port number>
icmp_code: <ICMP code>
icmp_type: <ICMP type>
protocol: <icmp4|icmp6|tcp|udp>
source: <source of traffic>
source_port: <source port number>
state: <enabled|disabled|logged>
```

````

````{group-tab} JSON

```
{
  "action": "<allow|reject|drop>",
  "description": "<description>",
  "destination": "<destination IP range>",
  "destination_port": "<destination port number>",
  "icmp_code": "<ICMP code>",
  "icmp_type": "<ICMP type>",
  "protocol": "<icmp4|icmp6|tcp|udp>",
  "source": "<source of traffic>",
  "source_port": "<source port number>",
  "state": "<enabled|disabled|logged>"
}
```

````
`````

- The `action` property is required.
- The `state` property defaults to `"enabled"` if unset.
- The `source` and `destination` properties can be specified as one or more CIDR blocks, IP ranges, or {ref}`selectors <network-acls-selectors>`. If left empty, they match any source or destination. Comma-separate multiple values.
- If the `protocol` is unset, it matches any protocol.
- The `"destination_port"` and `"source_port"` options and `"icmp_code"` and `"icmp_type"` options are mutually exclusive sets. Although both sets are shown in the same rule above to demonstrate the syntax, they never appear together in practice.
   - The `"destination_port"` and `"source_port"` options are only available when the `"protocol"` for the rule is `"tcp"` or `"udp"`.
   - The [`"icmp_code"`](https://www.iana.org/assignments/icmp-parameters/icmp-parameters.xhtml#icmp-parameters-codes) and [`"icmp_type"`](https://www.iana.org/assignments/icmp-parameters/icmp-parameters.xhtml#icmp-parameters-types) options are only available when the `"protocol"` is `"icmp4"` or `"icmp6"`.
- The `"state"` is `"enabled"` by default. The `"logged"` value is used to {ref}`log traffic <network-acls-log>` to a rule.

See {ref}`network-acls-rule-properties` for more information.

### Add a rule

`````{tabs}
````{group-tab} CLI

To add a rule to an ACL, run:

```bash
lxc network acl rule add <ACL_name> <egress|ingress> [properties...]
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

### Remove a rule

`````{tabs}
````{group-tab} CLI

To remove a rule from an ACL, run:

```bash
lxc network acl rule remove <ACL_name> <egress|ingress> [properties...]
```

You must either specify all properties needed to uniquely identify a rule or add `--force` to the command to delete all matching rules.

````
% End of group-tab CLI

````{group-tab} API

There is no specific endpoint for removing a rule. Instead, you must {ref}`edit the full ACL <network-acls-edit>`, which contains the `egress` and `ingress` lists.

````
% End of group-tab API

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

In ACL rules, the `source` and `destination` keys support using selectors instead of CIDR blocks or IP ranges. You can only use selectors in the `source` of `ingress` rules, and in the `destination` of `egress` rules.

Using selectors allows you to define rules for groups of instances instead of managing lists of IP addresses or subnets manually.

There are two types of selectors:

- subject name selectors (ACL groups)
- network subject selectors

(network-acls-selectors-subject-name)=
#### Subject name selectors (ACL groups)

Instance NICs that are assigned a particular ACL—either directly or through its network—form a logical port group. The name of that ACL can be used within the rules of other ACLs as a *subject name selector*.

For example, if you have an ACL with the name `my-acl`, you can specify the group of instance NICs that are assigned this ACL as a source by setting `source` to `my-acl`.

(network-acls-selectors-network-subject)=
#### Network subject selectors

Use *network subject selectors* to define rules based on the network that the traffic is coming from or going to.

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

If your network supports [network peers](network_ovn_peers.md), you can reference traffic to or from the peer connection by using a network subject selector in the format `@<network_name>/<peer_name>`. For example:

```yaml
source: "@ovn1/mypeer"
```

When using a network subject selector, the network that has the ACL assigned to it must have the specified peer connection.
Otherwise, the ACL cannot be assigned to it.

(network-acls-log)=
### Log traffic

ACL rules are primarily used to control network traffic between instances and networks. However, they can also be used to log specific types of traffic, which is useful for monitoring or testing rules before enabling them.

To configure a rule so that it only logs traffic, configure its `state` to `logged` when you {ref}`add the rule <network-acls-rules>` or {ref}`edit the ACL <network-acls-edit>`.

#### View logs

`````{tabs}
````{group-tab} CLI

To display the logs for all `logged` rules in an ACL, run:

```bash
lxc network acl show-log <ACL_name>
```

````
% End of CLI group-tab

````{group-tab} API

To display the logs for all `logged` rules in an ACL, query the following endpoint:

```bash
lxc query --request GET /1.0/network-acls/{name}/log
```

See [the API reference](swagger:/network-acls/network_acl_log_get) for more information.

##### Example

```
lxc query --request GET /1.0/network-acls/my-logged-acl/log
```

````
% End of API group-tab

`````

```{note}
LXD does not validate whether the specified ACL contains any rules with a `state` of `logged`. As a result, if your attempt to view logs returns no data, it could due to one of the following:
- The ACL includes one or more `logged` rules, but none have matched any traffic yet.
- The ACL does not contain any rules with a `state` of `logged`.
```

(network-acls-edit)=
## Edit an ACL

`````{tabs}
````{group-tab} CLI

Use the CLI to:

- {ref}`network-acls-edit-cli-rename`
- {ref}`network-acls-edit-cli-properties`

(network-acls-edit-cli-rename)=
### Rename an ACL via CLI

Requirements:

- You can only rename an ACL that is not currently assigned to a NIC or network. See the {ref}`network-acls-assign` section for more information.
- The new name must meet the naming requirements listed in {ref}`network-acls-create`.

To rename an ACL, query the following endpoint:

```bash
lxc network acl rename <old ACL name> <new ACL name>
```


(network-acls-edit-cli-properties)=
### Edit other properties via CLI

Run:

```bash
lxc network acl edit <ACL_name>
```

This command opens the ACL in YAML format for editing. You can edit any part of the configuration _except_ for the ACL name.

````

````{group-tab} API

Use the API to:

- {ref}`network-acls-edit-api-rename`
- {ref}`network-acls-edit-api-properties`
- {ref}`network-acls-edit-api-config`

(network-acls-edit-api-rename)=
### Rename an ACL via API

Requirements:

- You can only rename an ACL that is not currently assigned to a NIC or network. See the {ref}`network-acls-assign` section for more information.
- The new name must meet the naming requirements listed in {ref}`network-acls-create`.

To rename an ACL, query the following endpoint:

```bash
lxc query --request POST /1.0/network-acls/{name} --data '{
  "name": "<new ACL name>"
}'
```

See [the API reference](swagger:/network-acls/network_acl_post) for more information.

#### Example

Rename an ACL named `web-traffic` to `internal-web-traffic`:

```bash
lxc query --request POST /1.0/network-acls/web-traffic --data '{
  "name": "internal-web-traffic"
}'
```

(network-acls-edit-api-properties)=
### Edit other properties via API

To update any ACL property aside from `name`, query the following endpoint:

```bash
lxc query --request PUT /1.0/network-acls/{name} --data '{
  "config": {
    "user.<custom key name>": "<custom key value>"
  },
  "description": "<description of the ACL>",
  "egress": [<egress rule>, <another egress rule...>,...],
  "ingress": [<ingress rule>, <another ingress rule...>,...]
}'
```

```{warning}
Be careful! Any properties you omit from this request (aside from the ACL `name`) will be reset to defaults.
```

The `PUT` method used in this request performs a full replacement of the ACL's properties. With the exception of the `name` property, all other properties are overwritten by the data you provide. Any omitted properties are reset to default values. To preserve any specific properties when making an update, first {ref}`retrieve the current ACL properties <network-acls-show>`, then copy the values you want to keep into your request body. See [the API reference](swagger:/network-acls/network_acl_put) for more information.

If you only need to add or update a `config.user.*` key, see: {ref}`network-acls-edit-api-config`.

#### Example

Consider an ACL named `test` with the following properties (shown in JSON):

```json
{
  "name": "test",
  "config": {
    "user.type": "dev"
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
lxc query --request PUT /1.0/network-acls/test --data '{
  "egress": [
    {
      "action": "allow",
      "state": "enabled"
    }
  ]
}'
```

After the above query is run, the `test` ACL contains the following properties:

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
lxc query --request PUT /1.0/network-acls/test --data '{
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

(network-acls-edit-api-config)=
### Edit custom user keys

To add or update the custom `config.user.*` keys, query the following endpoint:

```bash
lxc query --request PATCH /1.0/network-acls/{name} --data '{
  "config": {
    "user.<custom key name>": "<custom key value>"
  }
}'
```

This `PATCH` endpoint allows you to add or update custom `config.user.*` keys without affecting other existing `config.user.*` entries.

However, this partial update behavior applies _only_ to the `config` property. For the `description`, `egress`, and `ingress` properties, this request behaves like a `PUT`: it replaces any provided values and resets any omitted properties to their defaults.

```{warning}
Be careful! Any properties you omit from this request (aside from `config` and `name`) will be reset to defaults.
```

#### Example

Consider an ACL named `test` with the following properties (shown in JSON):

```json
{
  "name": "test",
  "description": "My test ACL",
  "config": {
    "user.type": "test"
  },
  "description": ""
}
```

The following query adds a `config.user.limit` key with the value of `10`:

```bash
lxc query --request PATCH /1.0/network-acls/test --data '{
  "description": "My test ACL",
  "config": {
    "user.limit": "10"
  }
}'
```

After sending the above request, the `test` ACL's properties are updated to:

```json
{
  "name": "test",
  "description": "My test ACL",
  "config": {
    "user.type": "test",
    "user.limit": "10"
  },
  "description": ""
}
```

Note that the request _inserted_ the new `user.limit` key without affecting the pre-existing `user.type` key. Also notice that the `description` property was sent in the request; otherwise, it would have been reset to its default value of an empty string.

````

`````

(network-acls-delete)=
## Delete an ACL

You can only delete an ACL that is not assigned to a NIC or network. See the {ref}`network-acls-assign` section for more information on ACL assignment.

`````{tabs}
````{group-tab} CLI

To delete an ACL, run:

```bash
lxc network acl delete <ACL_name>
```

````
% End of group-tab CLI

````{group-tab} API

```bash
lxc query --request DELETE /1.0/network-acls/{name}
```

See [the API reference](swagger:/network-acls/network_acl_delete) for more information.

````
% End of group-tab API

`````

(network-acls-assign)=
## Assign an ACL

An ACL is inactive until it is assigned to one of the following targets:

- a {ref}`network-ovn`
- a {ref}`network-bridge`
- an {ref}`OVN NIC type of an instance <nic-ovn>`

To assign an ACL, you must update the `security.acls` option within its target's configuration.

```{tip}
Setting the `security.acls` option overwrites the existing value. To preserve any existing ACL rules, note their names and add them along with the new rule you want to add. Similarly, if you want to remove an existing ACL rule, add back all names _except_ the one you want to remove.
```

Assigning one or more ACLs to a NIC or network adds a default rule that rejects all unmatched traffic. See {ref}`network-acls-defaults` for details.

(network-acls-view-security)=
### View existing `security.acls`

`````{tabs}
````{group-tab} CLI

#### View existing rules for a network

Run:

```bash
lxc network get <network_name> security.acls
```

#### View existing rules for an instance NIC

Run:

```bash
lxc config device get <instance_name> <NIC_device_name> security.acls
```

##### Example

```bash
lxc config device get ubuntu-container ovn-nic security.acls
```

````
% End of group-tab CLI

````{group-tab} API

#### View existing rules for a network

Query the following endpoint:

```bash
lxc query --request GET /1.0/networks/{networkName} | jq -r '.config["security.acls"] // "NO ACLs set"'
```

See [the API reference](swagger:/networks/network_get) for more information.

##### Example

```bash
lxc query --request GET /1.0/networks/ovn1 | jq -r '.config["security.acls"] // "NO ACLs set"'
```

#### View existing rules for an instance NIC

Query the following endpoint:

```bash
lxc query --request GET /1.0/networks/{networkName} | jq -r '.devices["<NIC name>"]["security.acls"] // "NO ACLs set"'
```

To use this query, you must replace the `{networkName}` and the `<NIC name>`. See [the API reference](swagger:/networks/network_get) for more information.

##### Example

```bash
lxc query --request GET /1.0/instances/ubuntu-container | jq -r '.devices["ovn-nic"]["security.acls"] // "No ACLs set"'
```

````
% End of group-tab API

`````

### Assign an ACL to a bridge or OVN network

`````{tabs}
````{group-tab} CLI

Set the network's `security.acls` to a string that contains the ACL name or names you want to add. Comma-separate multiple names:

```bash
lxc network set <network_name> security.acls="<ACL_name>[,<ACL_NAME>,...]"
```

#### Examples

Set the `ovn1` network's `security.acls` to contain only the `web-traffic` ACL:

```bash
lxc network set ovn1 security.acls="web-traffic"
```

Set the `ovn1` network's `security.acls` to contain three ACLs:

```bash
lxc network set ovn1 security.acls="web-traffic,internal-traffic,ssh-access"
```

````
% End of group-tab CLI

````{group-tab} API

Send a request to set the network's `security.acls` to a string that contains the ACL name or names you want to add. Comma-separate multiple names:

```bash
lxc query --request PATCH /1.0/networks/{networkName} --data '{
  "config": {
    "security.acls": "<ACL_name>[,<ACL_name>,...]"
  }
}'
```

See [the API reference](swagger:/networks/network_patch) for more information.

#### Examples

Set the `ovn1` network's `security.acls` to contain only the `web-traffic` ACL:

```bash
lxc query --request PATCH /1.0/networks/ovn1 --data '{
  "config": {
    "security.acls": "web-traffic"
  }
}'
```

Set the `ovn1` network's `security.acls` to contain three ACLs:

```bash
lxc query --request PATCH /1.0/networks/ovn1 --data '{
  "config": {
    "security.acls": "web-traffic,internal-traffic,ssh-access"
  }
}'
```

````
% End of group-tab API

`````

### Assign an ACL to the NIC of an instance

For {abbr}`NICs (Network Interface Cards)`, you can only assign an ACL to a NIC with an {ref}`OVN NIC type <nic-ovn>`.

`````{tabs}
````{group-tab} CLI

To assign an ACL to an instance NIC, run:

```bash
lxc config device set <instance_name> <NIC_name> security.acls="<ACL_name>[,ACL_name,...]"
```

#### Example

Assign a single ACL to an instance NIC:

```bash
lxc config device set ubuntu-container ovn-nic security.acls="web-traffic"
```

Assign multiple ACLs to an instance NIC:

```bash
lxc config device set ubuntu-container ovn-nic security.acls="web-traffic,internal-traffic,ssh-access"
```

````
% End of group-tab CLI

````{group-tab} API

#### View the existing NIC configuration

To update the configuration for an instance's device using the API, you must include all the required keys for the device—even if you're only changing one key. For an instance NIC device, the required keys are the `type` of `nic` and the `network` name. Include these along with the key to update, and any other existing keys (unless you want to remove them). Omitted keys are reset to default values.

To view the existing instance NIC configuration, query the following endpoint:

```bash
lxc query /1.0/instances/{instanceName} | jq '.devices["<NIC name>"]'
```

See [the API reference](swagger:/instances/instance_get) for more information.

##### Example

```bash
lxc query /1.0/instances/ubuntu-container | jq '.devices["ovn-nic"]'
```

#### Assign the ACL

To assign an ACL to the instance NIC, set its `security.acls` to a string that contains the ACL name or names you want to add. Comma-separate multiple names:

```bash
lxc query --request PATCH /1.0/instances/{instanceName} --data '{
  "devices": {
    "<NIC name>": {
      "network": <network_name>,
      "type": "nic",
      "security.acls": "<ACL_name>[,<ACL_name>,...]",
      <other optional keys>
    }
  }
}'
```

See [the API reference](swagger:/instances/instance_patch) for more information.

##### Examples

For the `ubuntu_container` instance, set its `ovn-nic` device's `security.acls` to contain only the `web-traffic` ACL:

```bash
lxc query --request PATCH /1.0/instances/ubuntu-container --data '{
  "devices": {
    "ovn-nic": {
      "network": "ovntest",
      "type": "nic",
      "security.acls": "web-traffic"
    }
  }
}'
```

Set `security.acls` to contain three ACLs:

```bash
lxc query --request PATCH /1.0/instances/ubuntu-container --data '{
  "devices": {
    "ovn-nic": {
      "network": "ovntest",
      "type": "nic",
      "security.acls": "web-traffic,internal-traffic,ssh-access"
    }
  }
}'
```

````
% End of group-tab API

`````

(network-acls-assign-additional)=
### Additional properties

To view additional properties of the `security.acls` lists, refer to the configuration options for the target network or NIC:

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
lxc network set <network_name> security.acls.default.<egress|ingress>.action=<allow|reject|drop>
```

#### Example

To set the default action for inbound traffic to `allow` for all instances on the `ovntest` network, run:

```bash
lxc network set ovntest security.acls.default.ingress.action=allow
```

### Configure a default action for an instance NIC device

To set the default action for an instance NIC's egress or ingress traffic, run:

```bash
lxc config device set <instance_name> <NIC_name> security.acls.default.<egress|ingress>.action=<allow|reject|drop>
```

#### Example

To set the default action for inbound traffic to `allow` for the `ubuntu-container` instance's `ovn-nic` NIC device, run:

```bash
lxc config device set ubuntu-container ovn-nic security.acls.default.ingress.action=allow
```

````
% End of group-tab CLI

````{group-tab} API

### Configure a default action for a network

To set the default action for a network's egress or ingress traffic, query the following endpoint:

```bash
lxc query --request PATCH /1.0/networks/{networkName} --data '{
  "config": {
    "security.acls.default.egress.action": "<allow|reject|drop>",
    "security.acls.default.ingress.action": "<allow|reject|drop>",
  }
}'
```

See [the API reference](swagger:/networks/network_patch) for more information.

#### Example

Set the `ovntest` network's default egress action to `allow`:

```bash
lxc query --request PATCH /1.0/networks/ovntest --data '{
  "config": {
    "security.acls.default.egress.action": "allow"
  }
}'
```

### Configure a default action for an instance NIC device

#### View the existing NIC configuration

To update the configuration for an instance's device using the API, you must include all the required keys for the device—even if you're only changing one key. For an instance NIC device, the required keys are the `type` of `nic` and the `network` name. Include these along with the key to update, and any other existing keys (unless you want to remove them). Omitted keys are reset to default values.

To view the existing instance NIC configuration, query the following endpoint:

```bash
lxc query /1.0/instances/{instanceName} | jq '.devices["<NIC name>"]'
```

See [the API reference](swagger:/networks/network_get) for more information.

##### Example

```bash
lxc query /1.0/instances/ubuntu-container | jq '.devices["ovn-nic"]'
```

#### Configure the default action

To set the default action for an instance NIC's traffic, query the following endpoint:

```bash
lxc query --request PATCH /1.0/instances/{instanceName} --data '{
  "devices": {
    "<NIC name>": {
      "network": <network_name>,
      "type": "nic",
      "security.acls.default.<egress|ingress>.action": "<allow|reject|drop>"
      <other optional keys>
    }
  }
}'
```

See [the API reference](swagger:/instances/instance_patch) for more information.

#### Example

This request sets the default action for inbound traffic to `allow` for the `ovn-nic` device of the `ubuntu-container` instance:

```bash
lxc query --request PATCH /1.0/instances/ubuntu-container --data '{
  "devices": {
    "ovn-nic": {
      "network": "ovntest",
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

- Unlike OVN ACLs, bridge ACLs are applied only on the boundary between the bridge and the LXD host.
  This means they can only be used to apply network policies for traffic going to or from external networks.
  They cannot be used for to create {spellexception}`intra-bridge` firewalls, thus firewalls that control traffic between instances connected to the same bridge.
- {ref}`ACL groups and network selectors <network-acls-selectors>` are not supported.
- When using the `iptables` firewall driver, you cannot use IP range subjects (for example, `192.0.2.1-192.0.2.10`).
- Baseline network service rules are added before ACL rules (in their respective INPUT/OUTPUT chains), because we cannot differentiate between INPUT/OUTPUT and FORWARD traffic once we have jumped into the ACL chain.
  Because of this, ACL rules cannot be used to block baseline service rules.
