# Clustering

LXD can be run in clustering mode, where any number of LXD servers
share the same distributed database and can be managed uniformly using
the lxc client or the REST API.

Note that this feature was introduced as part of the API extension 
"clustering".

## Forming a cluster

First you need to choose a bootstrap LXD node. It can be an existing
LXD server or a brand new one. Then you need to initialize the
bootstrap node and join further nodes to the cluster. This can be done
interactively or with a preseed file.

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

### Interactively

Run `lxd init` and answer `yes` to the very first question ("Would you
like to use LXD clustering?"). Then choose a name for identifying the
node, and an IP or DNS address that other nodes can use to connect to
it, and answer `no` to the question about whether you're joining an
existing cluster. Finally, optionally create a storage pool and a
network bridge. At this point your first cluster node should be up and
available on your network.

You can now join further nodes to the cluster. Note however that these
nodes should be brand new LXD servers, or alternatively you should
clear their contents before joining, since any existing data on them
will be lost.

There are two ways to add a member to an existing cluster; using the trust password or using a join token.
A join token for a new member is generated in advance on the existing cluster using the command:

```
lxc cluster add <new member name>
```

This will return a single-use join token which can then be used in the join token question stage of `lxd init`.
The join token contains the addresses of the existing online members, as well as a single-use secret and the
fingerprint of the cluster certificate. This reduces the amount of questions you have to answer during `lxd init`
as the join token can be used to answer these questions automatically.

Alternatively you can use the trust password instead of using a join token.

To add an additional node, run `lxd init` and answer `yes` to the question about whether to use clustering.
Choose a node name that is different from the one chosen for the bootstrap node or any other nodes you have joined
so far. Then pick an IP or DNS address for the node and answer `yes` to the question about whether you're joining
an existing cluster.

If you have a join token then answer `yes` to the question that asks if you have a join token and then copy it in
when it asks for it.

If you do not have a join token, but have a trust password instead then, then answer `no` to the question that asks
if you have a join token. Then pick an address of an existing node in the cluster and check the fingerprint that
gets printed matches the cluster certificate of the existing members.

### Preseed

Create a preseed file for the bootstrap node with the configuration
you want, for example:

```yaml
config:
  core.trust_password: sekret
  core.https_address: 10.55.60.171:8443
  images.auto_update_interval: 15
storage_pools:
- name: default
  driver: dir
networks:
- name: lxdbr0
  type: bridge
  config:
    ipv4.address: 192.168.100.14/24
    ipv6.address: none
profiles:
- name: default
  devices:
    root:
      path: /
      pool: default
      type: disk
    eth0:
      name: eth0
      nictype: bridged
      parent: lxdbr0
      type: nic
cluster:
  server_name: node1
  enabled: true
```

Then run `cat <preseed-file> | lxd init --preseed` and your first node
should be bootstrapped.

Now create a bootstrap file for another node. You only need to fill in the
``cluster`` section with data and config values that are specific to the joining
node.

Be sure to include the address and certificate of the target bootstrap node. To
create a YAML-compatible entry for the ``cluster_certificate`` key you can use a
command like `sed ':a;N;$!ba;s/\n/\n\n/g' /var/lib/lxd/cluster.crt` (or
`sed ':a;N;$!ba;s/\n/\n\n/g' /var/snap/lxd/common/lxd/cluster.crt` for snap users), which you
have to run on the bootstrap node. `cluster_certificate_path` key (which should
contain valid path to cluster certificate) can be used instead of `cluster_certificate` key.

For example:

```yaml
cluster:
  enabled: true
  server_name: node2
  server_address: 10.55.60.155:8443
  cluster_address: 10.55.60.171:8443
  cluster_certificate: "-----BEGIN CERTIFICATE-----

opyQ1VRpAg2sV2C4W8irbNqeUsTeZZxhLqp4vNOXXBBrSqUCdPu1JXADV0kavg1l

2sXYoMobyV3K+RaJgsr1OiHjacGiGCQT3YyNGGY/n5zgT/8xI0Dquvja0bNkaf6f

...

-----END CERTIFICATE-----
"
  cluster_password: sekret
  member_config:
  - entity: storage-pool
    name: default
    key: source
    value: ""
```

When joining a cluster using a cluster join token, the following fields can be omitted:

 - server\_name
 - cluster\_address
 - cluster\_certificate
 - cluster\_password

And instead the full token be passed through the `cluster_token` field.

## Managing a cluster

Once your cluster is formed you can see a list of its nodes and their
status by running `lxc cluster list`. More detailed information about
an individual node is available with `lxc cluster show <node name>`.

### Cluster member configuration

Each cluster member has its own key/value configuration with the following supported namespaces:

- `user` (free form key/value for user metadata)
- `scheduler` (options related to how the member is automatically targeted by the cluster)

The currently supported keys are:

| Key                | Type   | Condition | Default | Description                                                                                                                                                                                  |
| :----------------- | :----- | :-------- | :------ | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| scheduler.instance | string | -         | all     | If `all` then the member will be auto-targeted for instance creation if it has the least number of instances. If `manual` then instances will only target the member if `--target` is given. |
| user.\*            | string | -         | -       | Free form user key/value storage (can be used in search)                                                                                                                                     |

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

### Deleting nodes

To cleanly delete a node from the cluster use `lxc cluster remove <node name>`.

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

### Upgrading nodes

To upgrade a cluster you need to upgrade all of its nodes, making sure
that they all upgrade to the same version of LXD.

To upgrade a single node, simply upgrade the lxd/lxc binaries on the
host (via snap or other packaging systems) and restart the lxd daemon.

If the new version of the daemon has database schema or API changes,
the restarted node might transition into a Blocked state. That happens
if there are still nodes in the cluster that have not been upgraded
and that are running an older version. When a node is in the
Blocked state it will not serve any LXD API requests (in particular,
lxc commands on that node will not work, although any running
instance will continue to run).

You can see if some nodes are blocked by running `lxc cluster list` on
a node which is not blocked.

As you proceed upgrading the rest of the nodes, they will all
transition to the Blocked state, until you upgrade the very last
one. At that point the blocked nodes will notice that there is no
out-of-date node left and will become operational again.

### Evacuating and restoring cluster members

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

### Failure domains

Failure domains can be used to indicate which nodes should be given preference
when trying to assign roles to a cluster member that has been shutdown or has
crashed. For example, if a cluster member that currently has the database role
gets shutdown, LXD will try to assign its database role to another cluster
member in the same failure domain, if one is available.

To change the failure domain of a cluster member you can use the `lxc cluster
edit <member>` command line tool, or the `PUT /1.0/cluster/<member>` REST API.

### Recover from quorum loss

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

Note that this time you have to use the regular ``lxc`` command line tool, not
``lxd``.

### Recover cluster members with changed addresses

If some members of your cluster are no longer reachable, or if the cluster itself
is unreachable due to a change in IP address or listening port number, the
cluster can be reconfigured.

On each member of the cluster, with LXD not running, run the following command:

```
lxd cluster edit
```

Note that all commands in this section will use ``lxd`` instead of ``lxc``.

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

## Instances

You can launch an instance on any node in the cluster from any node in
the cluster. For example, from node1:

```bash
lxc launch --target node2 ubuntu:18.04 bionic
```

will launch an Ubuntu 18.04 container on node2.

When you launch an instance without defining a target, the instance will be 
launched on the server which has the lowest number of instances.
If all the servers have the same amount of instances, it will choose one at random.

You can list all instances in the cluster with:

```bash
lxc list
```

The NODE column will indicate on which node they are running.

After an instance is launched, you can operate it from any node. For
example, from node1:

```bash
lxc exec bionic ls /
lxc stop bionic
lxc delete bionic
lxc pull file bionic/etc/hosts .
```

### Manually altering Raft membership

There might be situations in which you need to manually alter the Raft
membership configuration of the cluster because some unexpected behavior
occurred.

For example if you have a cluster member that was removed uncleanly it might not
show up in `lxc cluster list` but still be part of the Raft configuration (you
can see that with `lxd sql local "SELECT * FROM raft_nodes").

In that case you can run:

```bash
lxd cluster remove-raft-node <address>
```

to remove the leftover node.

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

## Storage pools

As mentioned above, all nodes must have identical storage pools. The
only difference between pools on different nodes might be their
`source`, `size` or `zfs.pool\_name` configuration keys.

To create a new storage pool, you first have to define it across all
nodes, for example:

```bash
lxc storage create --target node1 data zfs source=/dev/vdb1
lxc storage create --target node2 data zfs source=/dev/vdc1
```

Note that when defining a new storage pool on a node the only valid
configuration keys you can pass are the node-specific ones mentioned above.

At this point the pool hasn't been actually created yet, but just
defined (it's state is marked as Pending if you run `lxc storage list`).

Now run:

```bash
lxc storage create data zfs
```

and the storage will be instantiated on all nodes. If you didn't
define it on a particular node, or a node is down, an error will be
returned.

You can pass to this final ``storage create`` command any configuration key
which is not node-specific (see above).

## Storage volumes

Each volume lives on a specific node. The `lxc storage volume list`
includes a `NODE` column to indicate on which node a certain volume
resides.

Different volumes can have the same name as long as they live on
different nodes (for example image volumes). You can manage storage
volumes in the same way you do in non-clustered deployments, except
that you'll have to pass a `--target <node name>` parameter to volume
commands if more than one node has a volume with the given name.

For example:

```bash
# Create a volume on the node this client is pointing at
lxc storage volume create default web

# Create a volume with the same node on another node
lxc storage volume create default web --target node2

# Show the two volumes defined
lxc storage volume show default web --target node1
lxc storage volume show default web --target node2
```

## Networks

As mentioned above, all nodes must have identical networks defined.

The only difference between networks on different nodes might be their optional configuration keys.
When defining a new network on a specific clustered node the only valid optional configuration keys you can pass
are `bridge.external_interfaces` and `parent`, as these can be different on each node (see documentation about
[network configuration](networks.md) for a definition of each).

To create a new network, you first have to define it across all nodes, for example:

```bash
lxc network create --target node1 my-network
lxc network create --target node2 my-network
```

At this point the network hasn't been actually created yet, but just defined
(it's state is marked as Pending if you run `lxc network list`).

Now run:

```bash
lxc network create my-network
```

The network will be instantiated on all nodes. If you didn't define it on a particular node, or a node is down,
an error will be returned.

You can pass to this final ``network create`` command any configuration key which is not node-specific (see above).

## Separate REST API and clustering networks

You can configure different networks for the REST API endpoint of your clients
and for internal traffic between the nodes of your cluster (for example in order
to use a virtual address for your REST API, with DNS round robin).

To do that, you need to bootstrap the first node of the cluster using the
```cluster.https_address``` config key. For example, when using preseed:

```yaml
config:
  core.trust_password: sekret
  core.https_address: my.lxd.cluster:8443
  cluster.https_address: 10.55.60.171:8443
...
```

(the rest of the preseed YAML is the same as above).

To join a new node, first set its REST API address, for instance using the
```lxc``` client:

```bash
lxc config set core.https_address my.lxd.cluster:8443
```

and then use the ```PUT /1.0/cluster``` API endpoint as usual, specifying the
address of the joining node with the ```server_address``` field. If you use
preseed, the YAML payload would be exactly like the one above.

## Updating the cluster certificate
In a LXD cluster, all servers respond with the same shared certificate. This
is usually a standard self-signed certificate with an expiry set to 10 years.

If you wish to replace it with something else, for example a valid certificate
obtained through Let's Encrypt, `lxc cluster update-certificate` can be used
to replace the certificate on all servers in your cluster.
