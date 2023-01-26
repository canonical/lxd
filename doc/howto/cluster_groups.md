---
discourse: 12716
---

(howto-cluster-groups)=
# How to set up cluster groups

```{youtube} https://www.youtube.com/watch?v=t_3YJo_xItM
```

Cluster members can be assigned to {ref}`cluster-groups`.
By default, all cluster members belong to the `default` group.

To create a cluster group, use the `lxc cluster group create` command.
For example:

    lxc cluster group create gpu

To assign a cluster member to a specific group, use the `lxc cluster group assign` command.
For example:

    lxc cluster group assign server1 gpu

## Launch an instance on a cluster group member

With cluster groups, you can target an instance to run on one of the members of the cluster group, instead of targeting it to run on a specific member.

```{note}
[`scheduler.instance`](cluster-member-config) must be set to either `all` (the default) or `group` to allow instances to be targeted to a cluster group.

See {ref}`clustering-instance-placement` for more information.
```

To launch an instance on a member of a cluster group, follow the instructions in {ref}`cluster-target-instance`, but use the group name prefixed with `@` for the `--target` flag.
For example:

    lxc launch images:ubuntu/22.04 c1 --target=@gpu
