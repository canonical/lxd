---
myst:
  html_meta:
    description: Reference for LXD cluster configuration, including members and cluster links.
---

(ref-clusters)=
# Clusters

Cluster configuration includes multiple categories:

## Cluster member configuration

Each cluster member has its own configuration.

```{toctree}
:titlesonly:
cluster_member_config
```

## Cluster link configuration

Each cluster link has its own configuration.

```{toctree}
:titlesonly:
cluster_link_config
```

## Additional information

Server configuration: The server configuration is shared by all cluster members. See {ref}`server` for available server configuration options, including the {ref}`server-level cluster configuration options <server-options-cluster>`.

## Related topics

{{clustering_how}}

{{clustering_exp}}
