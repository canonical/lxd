(cluster-link-config)=
# Cluster link configuration

Each cluster link has its own key/value configuration with the following supported namespaces:

- {ref}`cluster-link-config-misc`
- {ref}`cluster-link-config-volatile`

(cluster-link-config-misc)=
## Miscellaneous options
The following keys are currently supported:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group cluster-link-conf start -->
    :end-before: <!-- config group cluster-link-conf end -->
```

(cluster-link-config-volatile)=
## Volatile internal data

```{warning}
The `volatile.*` keys cannot be manipulated by the user. Do not attempt to modify these keys in any way. LXD modifies these keys, and attempting to manipulate them yourself might break LXD in non-obvious ways.
```

The following volatile keys are currently used internally by LXD to store internal data specific to a cluster link:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group cluster-link-volatile-conf start -->
    :end-before: <!-- config group cluster-link-volatile-conf end -->
```

## Related topics

{{clustering_how}}

{{clustering_exp}}
