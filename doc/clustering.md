---
discourse: lxc:[LXD&#32;cluster&#32;on&#32;Raspberry&#32;Pi&#32;4](9076)
relatedlinks: "[MicroCloud](https://canonical.com/microcloud)"
myst:
  html_meta:
    description: An index of how-to guides for LXD clusters, covering forming and managing clusters, configuring cluster networking and storage, disaster recovery, and more.
---

(clustering)=
# Clustering

These how-to guides cover common operations related to clustering in LXD.

## Create and configure clusters

LXD servers can join together as members of a cluster, then be configured for features such as control plane mode and cluster healing.

```{toctree}
:titlesonly:

Form a cluster </howto/cluster_form>
Manage a cluster </howto/cluster_manage>
Configure networks </howto/cluster_config_networks>
Configure storage </howto/cluster_config_storage>
```

## Manage instances and cluster groups

Instances on cluster members can be accessed from and migrated to other members. Cluster groups and placement groups can be used to control how instances are distributed across cluster members.

```{toctree}
:titlesonly:

Manage instances </howto/cluster_manage_instance>
Set up cluster groups </howto/cluster_groups>
Use placement groups </howto/cluster_placement_groups>
```

## Recover clusters or cluster volumes

Quorum loss from member failures and orphaned volume entries from interrupted migrations can both be recovered.

```{toctree}
:titlesonly:

Recover a cluster </howto/cluster_recover>
Recover orphaned volume entries </howto/cluster_recover_volumes>
```

## Set up a virtual IP

A highly available virtual IP provides a stable entry point for client connections even if individual cluster members go offline.

```{toctree}
:titlesonly:

Set up a highly available virtual IP </howto/cluster_vip>
```

## Link clusters

Clusters can be linked together for authenticated communication, enabling features such as replicators.

```{toctree}
:titlesonly:

Create cluster links </howto/cluster_links_create>
Manage cluster links </howto/cluster_links_manage>
```

## Use replicators

Replicators continuously copy storage volumes from one LXD cluster to another for disaster recovery purposes.

```{toctree}
:titlesonly:

Set up replicators </howto/replicators_create>
Manage replicators </howto/replicators_manage>
```

## Related topics

{{clustering_exp}}

{{clustering_ref}}
