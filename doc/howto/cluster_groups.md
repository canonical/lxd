(cluster-groups)=
# How to set up cluster groups

Cluster members can be assigned to groups using the `lxc cluster group assign` command:

```bash
lxc cluster group create gpu
lxc cluster group assign cluster:node1 gpu
```

With cluster groups, it's possible to target specific groups instead of individual members.
This is done by using the `@` prefix when using `--target`.

An example:

```bash
lxc launch ubuntu:22.04 cluster:ubuntu --target=@gpu
```

This will cause the instance to be created on a cluster member belonging to `gpu` group if `scheduler.instance` is set to either `all` (default) or `group`.
