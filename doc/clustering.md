---
discourse: 9076,11330,12716
---

# Clustering

```{youtube} https://www.youtube.com/watch?v=nrOR6yaO_MY
```

LXD can be run in clustering mode, where any number of LXD servers
share the same distributed database and can be managed uniformly using
the `lxc` client or the REST API.

Note that this feature was introduced as part of the API extension
"clustering".

## Forming a cluster

Note that all further nodes joining the cluster must have identical
configuration to the bootstrap node, in terms of storage pools and
networks. The only configuration that can be node-specific are the
`source` and `size` keys for storage pools and the
`bridge.external_interfaces` key for networks.

It is strongly recommended that the number of nodes in the cluster be
at least three, so the cluster can survive the loss of at least one node
and still be able to establish quorum for its distributed state (which is
kept in a SQLite database replicated using the Raft algorithm). If the
number of nodes is less than three, then only one node in the cluster
will store the SQLite database. When the third node joins the cluster,
both the second and third nodes will receive a replica of the database.

### Per-server configuration

As mentioned previously, LXD cluster members are generally assumed to be identical systems.

However to accommodate things like slightly different disk ordering or
network interface naming, LXD records some settings as being
server-specific. When such settings are present in a cluster, any new
server being added will have to provide a value for it.

This is most often done through the interactive `lxd init` which will
ask the user for the value for a number of configuration keys related to
storage or networks.

Those typically cover:

- Source device for a storage pool (leaving empty would create a loop)
- Name for a ZFS zpool (defaults to the name of the LXD pool)
- External interfaces for a bridged network (empty would add none)
- Name of the parent network device for managed `physical` or `macvlan` networks (must be set)

It's possible to lookup the questions ahead of time (useful for scripting) by querying the `/1.0/cluster` API endpoint.
This can be done through `lxc query /1.0/cluster` or through other API clients.

## Managing a cluster

### Cluster member roles

The following roles can be assigned to LXD cluster members.
Automatic roles are assigned by LXD itself and cannot be modified by the user.

| Role                  | Automatic     | Description |
| :---                  | :--------     | :---------- |
| `database`            | yes           | Voting member of the distributed database |
| `database-leader`     | yes           | Current leader of the distributed database |
| `database-standby`    | yes           | Stand-by (non-voting) member of the distributed database |
| `event-hub`           | no            | Exchange point (hub) for the internal LXD events (requires at least two) |
| `ovn-chassis`         | no            | Uplink gateway candidate for OVN networks |

### Voting and stand-by members

The cluster uses a distributed [database](database.md) to store its state. All
nodes in the cluster need to access such distributed database in order to serve
user requests.

If the cluster has many nodes, only some of them will be picked to replicate
database data. Each node that is picked can replicate data either as "voter" or
as "stand-by". The database (and hence the cluster) will remain available as
long as a majority of voters is online. A stand-by node will automatically be
promoted to voter when another voter is shutdown gracefully or when its detected
to be offline.

The default number of voting nodes is 3 and the default number of stand-by nodes
is 2. This means that your cluster will remain operation as long as you switch
off at most one voting node at a time.

You can change the desired number of voting and stand-by nodes with:

```bash
lxc config set cluster.max_voters <n>
```

and

```bash
lxc config set cluster.max_standby <n>
```

with the constraint that the maximum number of voters must be odd and must be
least 3, while the maximum number of stand-by nodes must be between 0 and 5.

### Offline nodes and fault tolerance

At each time there will be an elected cluster leader that will monitor
the health of the other nodes. If a node is down for more than 20
seconds, its status will be marked as OFFLINE and no operation will be
possible on it, as well as operations that require a state change
across all nodes.

If the node that goes offline is the leader itself, the other nodes
will elect a new leader.

As soon as the offline node comes back online, operations will be
available again.

If you can't or don't want to bring the node back online, you can
delete it from the cluster using `lxc cluster remove --force <node name>`.

You can tweak the amount of seconds after which a non-responding node will be
considered offline by running:

```bash
lxc config set cluster.offline_threshold <n seconds>
```

The minimum value is 10 seconds.

### Failure domains

Failure domains can be used to indicate which nodes should be given preference
when trying to assign roles to a cluster member that has been shutdown or has
crashed. For example, if a cluster member that currently has the database role
gets shutdown, LXD will try to assign its database role to another cluster
member in the same failure domain, if one is available.

To change the failure domain of a cluster member you can use the `lxc cluster
edit <member>` command line tool, or the `PUT /1.0/cluster/<member>` REST API.

## Images

By default, LXD will replicate images on as many cluster members as you
have database members. This typically means up to 3 copies within the cluster.

That number can be increased to improve fault tolerance and likelihood
of the image being locally available.

The special value of "-1" may be used to have the image copied on all nodes.

You can disable the image replication in the cluster by setting the count down to 1:

```bash
lxc config set cluster.images_minimal_replica 1
```

## Cluster groups

In a LXD cluster, members can be added to cluster groups. By default, all members belong to the `default` group.


```{toctree}
:hidden:
:titlesonly:

explanation/clustering.md
Form a cluster <howto/cluster_form>
Manage a cluster <howto/cluster_manage>
Recover a cluster <howto/cluster_recover>
Manage instances <howto/cluster_manage_instance>
Configure storage <howto/cluster_config_storage>
Configure networks <howto/cluster_config_networks>
Set up cluster groups <howto/cluster_groups>
reference/cluster_member_config
```
