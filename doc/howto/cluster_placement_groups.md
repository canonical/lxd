(cluster-placement-groups)=
# How to use placement groups

Placement groups allow you to control how instances are distributed across cluster members.
You can either spread instances across different members for high availability, or compact them onto the same member(s) for performance and locality.

```{note}
Placement groups are only available in clustered LXD deployments and are scoped to individual projects.
```

## Create a placement group

Placement groups require two configuration keys: `policy` and `rigor`.

### Policy options

**Spread policy**
: Distributes instances across different cluster members to maximize availability and distribute load.

**Compact policy**
: Co-locates instances on the same cluster member to minimize network latency and maximize resource sharing.

### Rigor options

**Strict rigor**
: Enforces the placement policy strictly. Instance creation fails if the policy cannot be satisfied.

**Permissive rigor**
: Attempts to follow the placement policy but allows fallback if constraints cannot be met.

### Create with spread policy

`````{tabs}
```{group-tab} CLI
To create a placement group with a strict spread policy:

    lxc placement-group create my-pg-spread policy=spread rigor=strict

To create a placement group with a permissive spread policy that allows fallback:

    lxc placement-group create my-pg-spread policy=spread rigor=permissive
```

```{group-tab} API
To create a placement group with a strict spread policy, send a POST request:

    lxc query --request POST /1.0/placement-groups --data '{
      "name": "my-pg-spread",
      "config": {
        "policy": "spread",
        "rigor": "strict"
      }
    }'

To create a placement group with a permissive spread policy:

    lxc query --request POST /1.0/placement-groups --data '{
      "name": "my-pg-spread",
      "config": {
        "policy": "spread",
        "rigor": "permissive"
      }
    }'
```
`````

### Create with compact policy

`````{tabs}
```{group-tab} CLI
To create a placement group with a strict compact policy:

    lxc placement-group create my-pg-compact policy=compact rigor=strict

To create a placement group with a permissive compact policy that allows fallback:

    lxc placement-group create my-pg-compact policy=compact rigor=permissive
```

```{group-tab} API
To create a placement group with a strict compact policy, send a POST request:

    lxc query --request POST /1.0/placement-groups --data '{
      "name": "my-pg-compact",
      "config": {
        "policy": "compact",
        "rigor": "strict"
      }
    }'

To create a placement group with a permissive compact policy:

    lxc query --request POST /1.0/placement-groups --data '{
      "name": "my-pg-compact",
      "config": {
        "policy": "compact",
        "rigor": "permissive"
      }
    }'
```
`````

## Assign instances to a placement group

### During instance creation

`````{tabs}
```{group-tab} CLI
Specify the placement group when creating an instance:

    lxc launch ubuntu:24.04 my-instance -c placement.group=my-pg-spread
```

```{group-tab} API
To create an instance with a placement group, send a POST request:

    lxc query --request POST /1.0/instances --data '{
      "name": "my-instance",
      "image": "ubuntu:24.04",
      "config": {
        "placement.group": "my-pg-spread"
      }
    }'
```
`````

### For existing instances

`````{tabs}
```{group-tab} CLI
Add a placement group to an existing instance:

    lxc config set my-instance placement.group=my-pg-spread
```

```{group-tab} API
To add a placement group to an existing instance, send a PATCH request:

    lxc query --request PATCH /1.0/instances/my-instance --data '{
      "config": {
        "placement.group": "my-pg-spread"
      }
    }'
```
`````

```{note}
Changing the placement group of an existing instance does not move the instance.
The new placement policy applies only to future LXD scheduling events (e.g., evacuation).
```

### Using profiles

`````{tabs}
```{group-tab} CLI
Apply a placement group to all instances using a profile:

    lxc profile set default placement.group=my-pg-spread
```

```{group-tab} API
To set a placement group on a profile, send a PATCH request:

    lxc query --request PATCH /1.0/profiles/default --data '{
      "config": {
        "placement.group": "my-pg-spread"
      }
    }'
```
`````

## View placement groups

### List placement groups

`````{tabs}
```{group-tab} CLI
List all placement groups in the current project:

    lxc placement-group list

List placement groups from all projects:

    lxc placement-group list --all-projects
```

```{group-tab} API
To retrieve all placement groups in a project, send a GET request:

    lxc query --request GET /1.0/placement-groups

To retrieve placement groups from all projects, send a GET request:

    lxc query --request GET /1.0/placement-groups?recursion=1&all-projects=true
```
`````

### Show details of a placement group

`````{tabs}
```{group-tab} CLI
View details of a specific placement group:

    lxc placement-group show my-pg-spread

The `used_by` field shows all instances and profiles referencing this placement group.
```

```{group-tab} API
To retrieve details of a specific placement group, send a GET request:

    lxc query --request GET /1.0/placement-groups/my-pg-spread

The `used_by` field shows all instances and profiles referencing this placement group.
```
`````

## Modify a placement group

### Edit interactively

`````{tabs}
```{group-tab} CLI
Open the placement group configuration in your default editor:

    lxc placement-group edit my-pg-spread
```

```{group-tab} API
To update the full placement group configuration, send a PUT request:

    lxc query --request PUT /1.0/placement-groups/my-pg-spread --data '<placement_group_configuration>'
```
`````

### Update specific keys

`````{tabs}
```{group-tab} CLI
Change the policy:

    lxc placement-group set my-pg-spread policy=compact

Change the rigor:

    lxc placement-group set my-pg-spread rigor=permissive

Get a configuration value:

    lxc placement-group get my-pg-spread policy
```

```{group-tab} API
To update specific keys in a placement group, send a PATCH request:

    lxc query --request PATCH /1.0/placement-groups/my-pg-spread --data '{
      "config": {
        "policy": "compact",
        "rigor": "permissive"
      }
    }'

To retrieve a specific configuration value, send a GET request and parse the response:

    lxc query --request GET /1.0/placement-groups/my-pg-spread
```
`````

### Add user metadata

`````{tabs}
```{group-tab} CLI
Add custom metadata to a placement group:

    lxc placement-group set my-pg-spread user.department=engineering
    lxc placement-group set my-pg-spread user.cost-center=12345
```

```{group-tab} API
To add custom metadata to a placement group, send a PATCH request:

    lxc query --request PATCH /1.0/placement-groups/my-pg-spread --data '{
      "config": {
        "user.department": "engineering",
        "user.cost-center": "12345"
      }
    }'
```
`````

## Rename a placement group

`````{tabs}
```{group-tab} CLI
```bash
lxc placement-group rename my-pg-spread my-pg-ha
```

```{group-tab} API
To rename a placement group, send a POST request:

    lxc query --request POST /1.0/placement-groups/my-pg-spread --data '{
      "name": "my-pg-ha"
    }'
```
`````

## Delete a placement group

`````{tabs}
```{group-tab} CLI
    lxc placement-group delete my-pg-spread

To find what's using a placement group before deletion:

    lxc placement-group show my-pg-spread | grep used_by
```

```{group-tab} API
To delete a placement group, send a DELETE request:

    lxc query --request DELETE /1.0/placement-groups/my-pg-spread

To find what's using a placement group before deletion:

    lxc query --request GET /1.0/placement-groups/my-pg-spread
```
`````

```{note}
You cannot delete a placement group that is in use. Remove it from all instances and profiles first.
```

## Placement behavior

### During instance creation

When you create an instance with a placement group:

1. LXD filters cluster members according to the placement policy
1. From the filtered members, LXD selects the member with the fewest instances
1. If strict rigor is set and filtering returns no eligible members, instance creation fails
1. If permissive rigor is set and filtering returns no eligible members, LXD uses all available members

### Spread policy behavior

**Strict spread**
: Places at most one instance per cluster member
: Fails if there aren't enough eligible members

**Permissive spread**
: Spreads instances as evenly as possible
: Ensures instance count per member differs by at most one

### Compact policy behavior

**Strict compact**
: Places all instances on the same cluster member
: Fails if the preferred member is unavailable

**Permissive compact**
: Prefers to place all instances on the same cluster member
: Allows fallback to other members if the preferred member is unavailable

### During cluster evacuation

When evacuating a cluster member, LXD respects placement groups:

- **Spread policy**: Distributes evacuated instances across remaining members
- **Compact policy**: Attempts to keep instances from the same placement group together

If strict placement cannot be satisfied during evacuation, LXD falls back to the least-loaded member (unlike instance creation, which would fail).

## Troubleshooting

### Instance creation fails with strict rigor

If instance creation fails with a strict placement group:

1. Check available cluster members: `lxc cluster list`
1. Check instance distribution: `lxc list -c nL`
1. Consider using permissive rigor or adding more cluster members

## Related topics

- {ref}`clustering-instance-placement`
- {ref}`ref-placement-groups`
- {config:option}`instance-placement:placement.group`
