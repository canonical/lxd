(cluster-manage)=
# How to manage a cluster

Once your cluster is formed you can see a list of its nodes and their
status by running `lxc cluster list`. More detailed information about
an individual node is available with `lxc cluster show <node name>`.

You can change the desired number of voting and stand-by nodes with:

## Configure

```bash
lxc config set cluster.max_voters <n>
```

and

```bash
lxc config set cluster.max_standby <n>
```

## Deleting nodes

To cleanly delete a node from the cluster use `lxc cluster remove <node name>`.

## Upgrading nodes

To upgrade a cluster you need to upgrade all of its nodes, making sure
that they all upgrade to the same version of LXD.

To upgrade a single node, simply upgrade the `lxd`/`lxc` binaries on the
host (via snap or other packaging systems) and restart the LXD daemon.

If the new version of the daemon has database schema or API changes,
the restarted node might transition into a Blocked state. That happens
if there are still nodes in the cluster that have not been upgraded
and that are running an older version. When a node is in the
Blocked state it will not serve any LXD API requests (in particular,
`lxc` commands on that node will not work, although any running
instance will continue to run).

You can see if some nodes are blocked by running `lxc cluster list` on
a node which is not blocked.

As you proceed upgrading the rest of the nodes, they will all
transition to the Blocked state, until you upgrade the very last
one. At that point the blocked nodes will notice that there is no
out-of-date node left and will become operational again.

## Evacuating and restoring cluster members

Whether it's for routine maintenance like applying system updates requiring
a reboot or to perform hardware changes, you may sometimes want to empty a
given server of all its instances.

This can be done using `lxc cluster evacuate <NAME>` which will migrate all
instances on that server, moving them to other cluster members. The evacuated
cluster member will be transitioned to an "evacuated" state which will prevent
the creation of any instances on it.

Once maintenance is complete, `lxc cluster restore <NAME>` will move the server
back into a normal running state and will move its instances back from the servers
that were temporarily holding them.

The behavior for a given instance can be configured through the `cluster.evacuate`
instance configuration key. Instances will be shutdown cleanly, respecting the
`boot.host_shutdown_timeout` configuration key.

## Updating the cluster certificate

In a LXD cluster, all servers respond with the same shared certificate. This
is usually a standard self-signed certificate with an expiry set to 10 years.

If you wish to replace it with something else, for example a valid certificate
obtained through Let's Encrypt, `lxc cluster update-certificate` can be used
to replace the certificate on all servers in your cluster.
