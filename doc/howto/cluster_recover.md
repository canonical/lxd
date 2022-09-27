(cluster-recover)=
# How to recover a cluster

You can tweak the amount of seconds after which a non-responding node will be
considered offline by running:

```bash
lxc config set cluster.offline_threshold <n seconds>
```

## Recover from quorum loss

Every LXD cluster has up to 3 members that serve as database nodes. If you
permanently lose a majority of the cluster members that are serving as database
nodes (for example you have a 3-member cluster and you lose 2 members), the
cluster will become unavailable. However, if at least one database node has
survived, you will be able to recover the cluster.

In order to check which cluster members are configured as database nodes, log on
any survived member of your cluster and run the command:

```
lxd cluster list-database
```

This will work even if the LXD daemon is not running.

Among the listed members, pick the one that has survived and log into it (if it
differs from the one you have run the command on).

Now make sure the LXD daemon is not running and then issue the command:

```
lxd cluster recover-from-quorum-loss
```

At this point you can restart the LXD daemon and the database should be back
online.

Note that no information has been deleted from the database, in particular all
information about the cluster members that you have lost is still there,
including the metadata about their instances. This can help you with further
recovery steps in case you need to re-create the lost instances.

In order to permanently delete the cluster members that you have lost, you can
run the command:

```
lxc cluster remove <name> --force
```

Note that this time you have to use the regular `lxc` command line tool, not
`lxd`.

## Recover cluster members with changed addresses

If some members of your cluster are no longer reachable, or if the cluster itself
is unreachable due to a change in IP address or listening port number, the
cluster can be reconfigured.

On each member of the cluster, with LXD not running, run the following command:

```
lxd cluster edit
```

Note that all commands in this section will use `lxd` instead of `lxc`.

This will present a YAML representation of this node's last recorded information
about the rest of the cluster:

```yaml
# Latest dqlite segment ID: 1234

members:
  - id: 1             # Internal ID of the node (Read-only)
    name: node1       # Name of the cluster member (Read-only)
    address: 10.0.0.10:8443 # Last known address of the node (Writeable)
    role: voter             # Last known role of the node (Writeable)
  - id: 2
   name: node2
    address: 10.0.0.11:8443
    role: stand-by
  - id: 3
   name: node3
    address: 10.0.0.12:8443
    role: spare
```

Members may not be removed from this configuration, and a spare node cannot become
a voter, as it may lack a global database. Importantly, keep in mind that at least
2 nodes must remain voters (except in the case of a 2-member cluster, where 1 voter
suffices), or there will be no quorum.

Once the necessary changes have been made, repeat the process on each member of the
cluster. Upon reloading LXD on each member, the cluster in its entirety should be
back online with all nodes reporting in.

Note that no information has been deleted from the database, all information
about the cluster members and their instances is still there.

## Manually altering Raft membership

There might be situations in which you need to manually alter the Raft
membership configuration of the cluster because some unexpected behavior
occurred.

For example if you have a cluster member that was removed uncleanly it might not
show up in `lxc cluster list` but still be part of the Raft configuration (you
can see that with `lxd sql local "SELECT * FROM raft_nodes"`).

In that case you can run:

```bash
lxd cluster remove-raft-node <address>
```

to remove the leftover node.
