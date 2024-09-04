(cluster-recover)=
# How to recover a cluster

It might happen that one or several members of your cluster go offline or become unreachable.
If too many cluster members go offline, no operations will be possible on the cluster.
See {ref}`clustering-offline-members` and {ref}`cluster-automatic-evacuation` for more information.

If you can bring the offline cluster members back up, operation resumes as normal.
If the cluster members are lost permanently (e.g. disk failure), it is possible
to recover any remaining cluster members.

```{note}
When your cluster is in a state that needs recovery, most `lxc` commands do not
work because the LXD database does not respond when a majority of database
voters are inaccessible.

The commands to recover a cluster are provided directly by the LXD daemon (`lxd`)
because they modify database files directly instead of making requests to the
LXD daemon.

Run `lxd cluster --help` for an overview of all available commands.
```

## Database members

Every LXD cluster has a specific number of members (configured through {config:option}`server-cluster:cluster.max_voters`) that serve as voting members of the distributed database.
If you lose a majority of these cluster members (for example, you have a three-member cluster and you lose two members), the cluster loses quorum and becomes unavailable.

To determine which members have (or had) database roles, log on to any surviving member of your cluster and run the following command:

    sudo lxd cluster list-database

## Recover from quorum loss

If only one cluster member with the database role survives, complete the following
steps. See [Reconfigure the cluster](#reconfigure-the-cluster) below for recovering
more than one member.

1. Make sure that the LXD daemon is not running on the machine.
   For example, if you're using the snap:

       sudo snap stop lxd

1. Use the following command to reconfigure the database:

       sudo lxd cluster recover-from-quorum-loss

1. Start the LXD daemon again. For example, if you're using the snap:

       sudo snap start lxd

The database should now be back online.
No information has been deleted from the database.
All information about the cluster members that you have lost is still there, including the metadata about their instances.
This can help you with further recovery steps if you need to re-create the lost instances.

To permanently delete the cluster members that you have lost, force-remove them.
See {ref}`cluster-manage-delete-members`.

## Reconfigure the cluster

```{important}
It is highly recommended to take a backup of `/var/snap/lxd/common/lxd/database`
(for snap users) or `/var/lib/lxd/lxd/database` (otherwise) before reconfiguring
the cluster.
```

If some members of your cluster are no longer reachable, or if the cluster itself is unreachable due to a change in IP address or listening port number, you can reconfigure the cluster.

To do so, choose one database member to edit the cluster configuration.
Once the cluster edit is complete you will need to manually copy the reconfigured global database to every other surviving member.

You can change the IP addresses or listening port numbers for each member as required.
You cannot add or remove any members during this process.
The cluster configuration must contain the description of the full cluster.

You can edit the {ref}`clustering-member-roles` of the members, but with the following limitations:

- A cluster member that does not have a `database*` role cannot become a voter, because it might lack a global database.
- At least two members must remain voters (except in the case of a two-member cluster, where one voter suffices), or there will be no quorum.

Before performing the recovery, stop the LXD daemon on all surviving cluster members.
   For example, if you're using the snap:

    sudo snap stop lxd

Complete the following steps on one database member:

1. Run the following command:

       sudo lxd cluster edit

1. Edit the YAML representation of the information that this cluster member has about the rest of the cluster:

   ```yaml
   # Latest dqlite segment ID: 1234

   members:
     - id: 1             # Internal ID of the member (Read-only)
       name: server1     # Name of the cluster member (Read-only)
       address: 192.0.2.10:8443 # Last known address of the member (Writeable)
       role: voter              # Last known role of the member (Writeable)
     - id: 2             # Internal ID of the member (Read-only)
       name: server2     # Name of the cluster member (Read-only)
       address: 192.0.2.11:8443 # Last known address of the member (Writeable)
       role: stand-by           # Last known role of the member (Writeable)
     - id: 3             # Internal ID of the member (Read-only)
       name: server3     # Name of the cluster member (Read-only)
       address: 192.0.2.12:8443 # Last known address of the member (Writeable)
       role: spare              # Last known role of the member (Writeable)
   ```

   You can edit the addresses and the roles.

1. When the cluster configuration has been changed on one member, LXD will create
   a tarball of the global database (`/var/snap/lxd/common/lxd/database/lxd_recovery_db.tar.gz`
   for snap installations or `/var/lib/lxd/database/lxd_recovery_db.tar.gz`).
   Copy this recovery tarball to the same path on all remaining cluster members.

   ```{note}
   The tarball can be removed from the first member after it is generated, but
   it does not have to be.
   ```

1. Once the tarball has been copied to all remaining cluster members, start the
   LXD daemon on all members again. LXD will load the recovery tarball on startup.

   If you're using the snap:

       sudo snap start lxd

The cluster should now be fully available again with all surviving members reporting in.
No information has been deleted from the database.
All information about the cluster members and their instances is still there.

## Manually alter Raft membership

In some situations, you might need to manually alter the Raft membership configuration of the cluster because of some unexpected behavior.

For example, if you have a cluster member that was removed uncleanly, it might not show up in [`lxc cluster list`](lxc_cluster_list.md) but still be part of the Raft configuration.
To see the Raft configuration, run the following command:

    lxd sql local "SELECT * FROM raft_nodes"

In that case, run the following command to remove the leftover node:

    lxd cluster remove-raft-node <address>
