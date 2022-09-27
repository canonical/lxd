(cluster-config-networks)=
# How to configure networks for a cluster

As mentioned above, all nodes must have identical networks defined.

The only difference between networks on different nodes might be their optional configuration keys.
When defining a new network on a specific clustered node the only valid optional configuration keys you can pass
are `bridge.external_interfaces` and `parent`, as these can be different on each node (see documentation about
[network configuration](../networks.md) for a definition of each).

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

You can pass to this final `network create` command any configuration key which is not node-specific (see above).

## Separate REST API and clustering networks

You can configure different networks for the REST API endpoint of your clients
and for internal traffic between the nodes of your cluster (for example in order
to use a virtual address for your REST API, with DNS round robin).

To do that, you need to bootstrap the first node of the cluster using the
`cluster.https_address` configuration key. For example, when using preseed:

```yaml
config:
  core.trust_password: sekret
  core.https_address: my.lxd.cluster:8443
  cluster.https_address: 10.55.60.171:8443
...
```

(the rest of the preseed YAML is the same as above).

To join a new node, first set its REST API address, for instance using the
`lxc` client:

```bash
lxc config set core.https_address my.lxd.cluster:8443
```

and then use the ```PUT /1.0/cluster``` API endpoint as usual, specifying the
address of the joining node with the ```server_address``` field. If you use
preseed, the YAML payload would be exactly like the one above.
