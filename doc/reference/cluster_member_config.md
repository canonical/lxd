(cluster-member-config)=
# Cluster member configuration

Each cluster member has its own key/value configuration with the following supported namespaces:

- `user` (free form key/value for user metadata)
- `scheduler` (options related to how the member is automatically targeted by the cluster)

The following keys are currently supported:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group cluster-cluster start -->
    :end-before: <!-- config group cluster-cluster end -->
```

## Related topics

{{clustering_how}}

{{clustering_exp}}
