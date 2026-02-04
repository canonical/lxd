(ref-placement-groups)=
# Placement group configuration

Placement groups can be configured through a set of key/value configuration options.
See {ref}`cluster-placement-groups` for instructions on how to create and manage placement groups.

The key/value configuration is namespaced.
The following options are available:

- {ref}`placement-group-config`

(placement-group-config)=
## Placement group options

Placement groups require two configuration keys to control instance placement behavior across cluster members.

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group placement-group-placement-group start -->
    :end-before: <!-- config group placement-group-placement-group end -->
```

## Related topics

{{clustering_how}}

{{clustering_exp}}
