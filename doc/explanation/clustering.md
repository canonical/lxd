---
discourse: 15871
---

(exp-clustering)=
# About clustering

```{youtube} https://www.youtube.com/watch?v=nrOR6yaO_MY
```

To spread the total workload over several servers, LXD can be run in clustering mode.
In this scenario, any number of LXD servers share the same distributed database that holds the configuration for the cluster members and their instances.
The LXD cluster can be managed uniformly using the `lxc` client or the REST API.

This feature was introduced as part of the [`clustering`](api-extensions.md#clustering) API extension and is available since LXD 3.0.

```{tip}
If you want to quickly set up a basic LXD cluster, check out [MicroCloud](https://discuss.linuxcontainers.org/t/introducing-microcloud/15871).
```

(clustering-members)=
## Cluster members

A LXD cluster consists of one bootstrap server and at least two further cluster members.
It stores its state in a [distributed database](../database.md), which is a [Dqlite](https://dqlite.io/) database replicated using the Raft algorithm.

While you could create a cluster with only two members, it is strongly recommended that the number of cluster members be at least three.
With this setup, the cluster can survive the loss of at least one member and still be able to establish quorum for its distributed state.

When you create the cluster, the Dqlite database runs on only the bootstrap server until a third member joins the cluster.
Then both the second and the third server receive a replica of the database.

See {ref}`cluster-form` for more information.

(clustering-member-roles)=
### Member roles

In a cluster with three members, all members replicate the distributed database that stores the state of the cluster.
If the cluster has more members, only some of them replicate the database.
The remaining members have access to the database, but don't replicate it.

At each time, there is an elected cluster leader that monitors the health of the other members.

Each member that replicates the database has either the role of a *voter* or of a *stand-by*.
If the cluster leader goes offline, one of the voters is elected as the new leader.
If a voter member goes offline, a stand-by member is automatically promoted to voter.
The database (and hence the cluster) remains available as long as a majority of voters is online.

The following roles can be assigned to LXD cluster members.
Automatic roles are assigned by LXD itself and cannot be modified by the user.

| Role                  | Automatic     | Description |
| :---                  | :--------     | :---------- |
| `database`            | yes           | Voting member of the distributed database |
| `database-leader`     | yes           | Current leader of the distributed database |
| `database-standby`    | yes           | Stand-by (non-voting) member of the distributed database |
| `event-hub`           | no            | Exchange point (hub) for the internal LXD events (requires at least two) |
| `ovn-chassis`         | no            | Uplink gateway candidate for OVN networks |

The default number of voter members ([`cluster.max_voters`](server)) is three.
The default number of stand-by members ([`cluster.max_standby`](server)) is two.
With this configuration, your cluster will remain operational as long as you switch off at most one voting member at a time.

See {ref}`cluster-manage` for more information.

(clustering-offline-members)=
#### Offline members and fault tolerance

If a cluster member is down for more than the configured offline threshold, its status is marked as offline.
In this case, no operations are possible on this member, and neither are operations that require a state change across all members.

As soon as the offline member comes back online, operations are available again.

If the member that goes offline is the leader itself, the other members will elect a new leader.

If you can't or don't want to bring the server back online, you can [delete it from the cluster](cluster-manage-delete-members).

You can tweak the amount of seconds after which a non-responding member is considered offline by setting the [`cluster.offline_threshold`](server) configuration.
The default value is 20 seconds.
The minimum value is 10 seconds.

See {ref}`cluster-recover` for more information.

#### Failure domains

You can use failure domains to indicate which cluster members should be given preference when assigning roles to a cluster member that has gone offline.
For example, if a cluster member that currently has the database role gets shut down, LXD tries to assign its database role to another cluster member in the same failure domain, if one is available.

To update the failure domain of a cluster member, use the `lxc cluster edit <member>` command and change the `failure_domain` property from `default` to another string.

(clustering-member-config)=
### Member configuration

LXD cluster members are generally assumed to be identical systems.
This means that all LXD servers joining a cluster must have an identical configuration to the bootstrap server, in terms of storage pools and networks.

To accommodate things like slightly different disk ordering or network interface naming, there is an exception for some configuration options related to storage and networks, which are member-specific.

When such settings are present in a cluster, any server that is being added must provide a value for them.
Most often, this is done through the interactive `lxd init` command, which asks the user for the value for a number of configuration keys related to storage or networks.

Those settings typically include:

- The source device and size for a storage pool
- The name for a ZFS zpool, LVM thin pool or LVM volume group
- External interfaces and BGP next-hop for a bridged network
- The name of the parent network device for managed `physical` or `macvlan` networks

See {ref}`cluster-config-storage` and {ref}`cluster-config-networks` for more information.

If you want to look up the questions ahead of time (which can be useful for scripting), query the `/1.0/cluster` API endpoint.
This can be done through `lxc query /1.0/cluster` or through other API clients.

## Images

By default, LXD replicates images on as many cluster members as there are database members.
This typically means up to three copies within the cluster.

You can increase that number to improve fault tolerance and the likelihood of the image being locally available.
To do so, set the [`cluster.images_minimal_replica`](server) configuration.
The special value of `-1` can be used to have the image copied to all cluster members.

(cluster-groups)=
## Cluster groups

In a LXD cluster, you can add members to cluster groups.
You can use these cluster groups to launch instances on a cluster member that belongs to a subset of all available members.
For example, you could create a cluster group for all members that have a GPU and then launch all instances that require a GPU on this cluster group.

By default, all cluster members belong to the `default` group.

See {ref}`howto-cluster-groups` and {ref}`cluster-target-instance` for more information.

(clustering-assignment)=
## Automatic assignment of instances

In a cluster setup, each instance lives on one of the cluster members.
When you launch an instance, you can target it to a specific cluster member, to a cluster group or have LXD automatically assign it to a cluster member.

By default, the automatic assignment picks the cluster member that has the lowest number of instances.
If several members have the same amount of instances, one of the members is chosen at random.

However, you can control this behavior with the [`scheduler.instance`](cluster-member-config) configuration option:

- If `scheduler.instance` is set to `all` for a cluster member, this cluster member is selected for an instance if:

   - The instance is created without `--target` and the cluster member has the lowest number of instances.
   - The instance is targeted to live on this cluster member.
   - The instance is targeted to live on a member of a cluster group that the cluster member is a part of, and the cluster member has the lowest number of instances compared to the other members of the cluster group.

- If `scheduler.instance` is set to `manual` for a cluster member, this cluster member is selected for an instance if:

   - The instance is targeted to live on this cluster member.

- If `scheduler.instance` is set to `group` for a cluster member, this cluster member is selected for an instance if:

   - The instance is targeted to live on this cluster member.
   - The instance is targeted to live on a member of a cluster group that the cluster member is a part of, and the cluster member has the lowest number of instances compared to the other members of the cluster group.
