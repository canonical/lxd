(cluster-config-storage)=
# How to configure storage for a cluster

As mentioned above, all nodes must have identical storage pools. The
only difference between pools on different nodes might be their
`source`, `size` or `zfs.pool_name` configuration keys.

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

You can pass to this final `storage create` command any configuration key
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
