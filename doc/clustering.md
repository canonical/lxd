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

To add an additional node, run `lxd init` and answer `yes` to the question
about whether to use clustering. Choose a node name that is different from
the one chosen for the bootstrap node or any other nodes you have joined so
far. Then pick an IP or DNS address for the node and answer `yes` to the
question about whether you're joining an existing cluster. Pick an address
of an existing node in the cluster and check the fingerprint that gets
printed.

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
command like `sed ':a;N;$!ba;s/\n/\n\n/g' /var/lib/lxd/server.crt`, which you
have to run on the bootstrap node.

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

## Managing a cluster

Once your cluster is formed you can see a list of its nodes and their
status by running `lxc cluster list`. More detailed information about
an individual node is available with `lxc cluster show <node name>`.

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

Note that this time you have to use the regular ```lxc``` command line tool, not
```lxd```.

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

As mentioned above, all nodes must have identical networks defined. The only
difference between networks on different nodes might be their
`bridge.external_interfaces` optional configuration key (see also documentation
about [network configuration](networks.md)).

To create a new network, you first have to define it across all
nodes, for example:

```bash
lxc network create --target node1 my-network
lxc network create --target node2 my-network
```

Note that when defining a new network on a node the only valid configuration
key you can pass is `bridge.external_interfaces`, as mentioned above.

At this point the network hasn't been actually created yet, but just
defined (it's state is marked as Pending if you run `lxc network list`).

Now run:

```bash
lxc network create my-network
```

and the network will be instantiated on all nodes. If you didn't
define it on a particular node, or a node is down, an error will be
returned.

You can pass to this final ``network create`` command any configuration key
which is not node-specific (see above).

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
