---
discourse: lxc:[Cluster&#32;member&#32;evacuation](11330)
---

(cluster-manage)=
# How to manage a cluster

After your cluster is formed, use [`lxc cluster list`](lxc_cluster_list.md) to see a list of its members and their status:

```{terminal}
:input: lxc cluster list
:scroll:

+---------+----------------------------+------------------+--------------+----------------+-------------+--------+-------------------+
| NAME    |            URL             |      ROLES       | ARCHITECTURE | FAILURE DOMAIN | DESCRIPTION | STATE  |      MESSAGE      |
+---------+----------------------------+------------------+--------------+----------------+-------------+--------+-------------------+
| server1 | https://192.0.2.101:8443   | database-leader  | x86_64       | default        |             | ONLINE | Fully operational |
|         |                            | database         |              |                |             |        |                   |
+---------+----------------------------+------------------+--------------+----------------+-------------+--------+-------------------+
| server2 | https://192.0.2.102:8443   | database-standby | aarch64      | default        |             | ONLINE | Fully operational |
+---------+----------------------------+------------------+--------------+----------------+-------------+--------+-------------------+
| server3 | https://192.0.2.103:8443   | database-standby | aarch64      | default        |             | ONLINE | Fully operational |
+---------+----------------------------+------------------+--------------+----------------+-------------+--------+-------------------+
```

To see more detailed information about an individual cluster member, run the following command:

    lxc cluster show <member_name>

To see state and usage information for a cluster member, run the following command:

    lxc cluster info <member_name>

## Configure your cluster

To configure your cluster, use [`lxc config`](lxc_config.md):

    lxc config set <server-config-option> <value>

Example:

    lxc config set cluster.max_voters 5

All LXD {ref}`server configuration options <server>` can be applied to cluster members.

Keep in mind that some options are global in scope, and others are local. When you configure an option with global scope on any cluster member, the changes are propagated to the other cluster members through the distributed database. The locally scoped options are set only on the cluster member where you configure them, unless you use the `--target` flag to specify a different cluster member.

In addition to the server configuration, there are {ref}`cluster member configuration options <cluster-member-config>` that are specific to each cluster member. To set these configuration values, use [`lxc cluster set`](lxc_cluster_set.md):

    lxc cluster set <member-name> <member-config-option> <value>

Example:

    lxc cluster set server1 scheduler.instance manual

Alternatively, you can use the {ref}`use the edit command <cluster-edit>`.

### Assign member roles

To add or remove a {ref}`member role <clustering-member-roles>` for a cluster member, use the [`lxc cluster role`](lxc_cluster_role.md) command:

    lxc cluster role add <member-name> <role>

Example:

    lxc cluster role add server1 event-hub

```{note}
You can add or remove only those roles that are not assigned automatically by LXD. To find out which roles are automatically assigned, see: {ref}`clustering-member-roles`.
```

(cluster-edit)=
### Edit the cluster member configuration

To edit all properties of a cluster member, including the member-specific configuration, the member roles, the failure domain and the cluster groups, use the following command:

    lxc cluster edit

For more information, see: [`lxc cluster edit`](lxc_cluster_edit.md).

(cluster-evacuate-restore)=
## Evacuate and restore cluster members

There are scenarios where you might need to empty a given cluster member of all its instances (for example, for routine maintenance like applying system updates that require a reboot, or to perform hardware changes). The {ref}`evacuate <cluster-evacuate>` and {ref}`restore <cluster-restore>` commands simplify this process.

(cluster-evacuate)=
### Evacuate a cluster member

The evacuation process migrates all instances on a given cluster member to other members in its cluster. The given member is then set to an "evacuated" state, which prevents the creation of any instances on it.

To begin this process, use the [`lxc cluster evacuate`](lxc_cluster_evacuate.md) command:

    lxc cluster evacuate <member_name>

(cluster-restore)=
### Restore an evacuated cluster member

When the evacuated cluster member is available again, use the [`lxc cluster restore`](lxc_cluster_restore.md) command to return it to a normal running state:

    lxc cluster restore <member_name>

This command removes the cluster member's "evacuated" state, migrates the evacuated instances back from the cluster members that were temporarily holding them (using live migration if applicable), then restarts any instances that were shut down.

(cluster-evacuation-mode)=
### Evacuation mode and live migration

You can control how each instance is migrated, via the {config:option}`instance-miscellaneous:cluster.evacuate` instance configuration key. This key applies to the migrations performed during both evacuation and restoration. By default, any instances that are suitable for {ref}`live migration <live-migration>` will be live-migrated, and any that are not suitable will be shut down. See the {config:option}`instance-miscellaneous:cluster.evacuate` reference documentation for further information.

If an instance is not suitable for live migration, it will be shut down cleanly before evacuation, respecting the {config:option}`instance-boot:boot.host_shutdown_timeout` configuration key.

```{note}
Any instance that you plan to live-migrate must have its {config:option}`instance-migration:migration.stateful` configuration option set to `true`. Be aware that this option can only be set while the instance is stopped. Thus, for any instance to have the ability to be live-migrated in the future, this option must be set to `true` ahead of time.
```

(cluster-automatic-evacuation)=
### Automatic evacuation

If you set the {config:option}`server-cluster:cluster.healing_threshold` configuration to a non-zero value, instances are automatically evacuated if a cluster member goes offline.

When the evacuated server is available again, you must manually restore it.

(cluster-manage-delete-members)=
## Delete cluster members

To cleanly delete a member from the cluster, use the following command:

    lxc cluster remove <member_name>

You can only cleanly delete members that are online and that don't have any instances located on them.

### Deal with offline cluster members

If a cluster member goes permanently offline, you can force-remove it from the cluster.
Make sure to do so as soon as you discover that you cannot recover the member.
If you keep an offline member in your cluster, you might encounter issues when upgrading your cluster to a newer version.

To force-remove a cluster member, enter the following command on one of the cluster members that is still online:

    lxc cluster remove --force <member_name>

```{caution}
Force-removing a cluster member will leave the member's database in an inconsistent state (for example, the storage pool on the member will not be removed).
As a result, it will not be possible to re-initialize LXD later, and the server must be fully reinstalled.
```

(howto-cluster-manage-update-upgrade)=
## Update or upgrade cluster members

To update or upgrade a cluster, you must perform the same operation on all of its members, ensuring that they all use the same version of LXD.

```{caution}
Do not attempt to update or upgrade your cluster if any of its members are offline.
Offline members cannot be updated or upgraded, and your cluster will end up in a blocked state.

Also note that if you are using the snap, updates might happen automatically, so to prevent any issues you should always recover or remove offline members immediately.
```

To update or upgrade the cluster, you must apply the change to each cluster member's LXD installation. If you are using the snap, see {ref}`howto-snap-updates` for update instructions about updates, and {ref}`howto-snap-change` for upgrade instructions.

If the new version of the daemon has database schema or API changes, the upgraded member might transition into a "blocked" state.
In this case, the member does not serve any LXD API requests (which means that `lxc` commands don't work on that member anymore), but any running instances will continue to run.

This happens if there are other cluster members that have not been updated or upgraded, resulting in mismatched versions.
Run [`lxc cluster list`](lxc_cluster_list.md) on a cluster member that is not blocked to see if any members are blocked.

As you proceed updating or upgrading the rest of the cluster members, they will all transition to the "blocked" state.
When you update or upgrade the last member, the blocked members will notice that all LXD versions now match, and the blocked members become operational again.

## Update the cluster certificate

In a LXD cluster, the API on all servers responds with the same shared certificate, which is usually a standard self-signed certificate with an expiry set to ten years.

The certificate is stored at `/var/snap/lxd/common/lxd/cluster.crt` (if you use the snap) or `/var/lib/lxd/cluster.crt` (otherwise) and is the same on all cluster members.

You can replace the standard certificate with another one, such as a valid certificate obtained through ACME services (see {ref}`authentication-server-certificate` for more information).
To do so, run the following command on any cluster member:

    lxc cluster update-certificate

This command replaces the certificate on all cluster members. For more information, see: [`lxc cluster update-certificate`](lxc_cluster_update-certificate.md).
