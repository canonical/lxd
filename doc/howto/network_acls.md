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

The instance NICs that have a particular ACL applied (either explicitly or implicitly through a network) make up a logical group, which can be referenced from other rules as a source or destination.
See {ref}`network-acls-groups` for more information.

## List ACLs

`````{tabs}
````{group-tab} CLI

To list all ACLs, run:

```
lxc network acl list
```

````

````{group-tab} API

To list all ACLs, query the `/1.0/network-acls` endpoint:

```
lxc query --request GET /1.0/network-acls
```

See [the API reference](swagger:/network-acls/network_acls_get) for more information.

You can also use {ref}`recursion <rest-api-recursion>` to list the ACLs with a higher level of detail:

```
lxc query --request GET /1.0/network-acls?recursion=1
```

````
`````

(network-acls-show)=
## Show an ACL

`````{tabs}
````{group-tab} CLI

To show details about a specific ACL, run:

```
lxc network acl show <acl_name>
```

Example:

```
lxc network acl show my-acl
```

````

````{group-tab} API

For details about a specific ACL, query the following endpoint:

```
lxc query --request GET /1.0/network-acls/{name}
```

See [the API reference](swagger:/network-acls/network_acl_get) for more information.

Example:

```
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
## Add or remove rules

Each ACL contains two lists of rules:

- *Ingress* rules apply to inbound traffic going towards the NIC.
- *Egress* rules apply to outbound traffic leaving the NIC.

To add a rule to an ACL, use the following command, where `<direction>` can be either `ingress` or `egress`:

```bash
lxc network acl rule add <ACL_name> <direction> [properties...]
```

This command adds a rule to the list for the specified direction.

You cannot edit a rule (except if you {ref}`edit the full ACL <network-acls-edit>`), but you can delete rules with the following command:

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

(network-acls-log)=
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
### Update `config.user.*` keys

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

(network-acls-assign)=
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