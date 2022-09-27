(cluster-manage-instance)=
# How to manage instances in a cluster

You can launch an instance on any node in the cluster from any node in
the cluster. For example, from node1:

```bash
lxc launch --target node2 ubuntu:22.04 c1
```

will launch an Ubuntu 22.04 container on node2.

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
lxc exec c1 ls /
lxc stop c1
lxc delete c1
lxc pull file c1/etc/hosts .
```
