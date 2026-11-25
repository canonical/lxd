(permissions-reference)=
# Permissions

A permission is an entitlement granted against a specific LXD API resource.
When managing user access via fine-grained authorization, you add identities to groups and then assign permissions to the groups.

Each LXD resource has an entity type, and the following sections list entitlements that can be granted against each type.
For a detailed explanation of LXD permissions, see {ref}`fine-grained-authorization`.

```{caution}
At some levels, certain entitlements cascade down to other resources.
See {ref}`authorization-entitlement-cascade`.
```

(permissions-server)=
## Server
> Entity type name: `server`

The `server` entity type is the top-level entity type for the LXD system.
Entitlements that are granted at this level might cascade to projects and other resources:

```{include} ../metadata.txt
    :start-after: <!-- entity group server start -->
    :end-before: <!-- entity group server end -->
```

(permissions-server-level)=
## Server-scoped resources

Server-scoped resources are owned directly by the LXD server.

### Certificate
> Entity type name: `certificate`

```{include} ../metadata.txt
    :start-after: <!-- entity group certificate start -->
    :end-before: <!-- entity group certificate end -->
```

### Cluster link
> Entity type name: `cluster_link`

```{include} ../metadata.txt
    :start-after: <!-- entity group cluster_link start -->
    :end-before: <!-- entity group cluster_link end -->
```

### Group
> Entity type name: `group`

```{include} ../metadata.txt
    :start-after: <!-- entity group group start -->
    :end-before: <!-- entity group group end -->
```

### Identity
> Entity type name: `identity`

```{include} ../metadata.txt
    :start-after: <!-- entity group identity start -->
    :end-before: <!-- entity group identity end -->
```

### Identity provider group
> Entity type name: `identity_provider_group`

```{include} ../metadata.txt
    :start-after: <!-- entity group identity_provider_group start -->
    :end-before: <!-- entity group identity_provider_group end -->
```

### Image registry
> Entity type name: `image_registry`

```{include} ../metadata.txt
    :start-after: <!-- entity group image_registry start -->
    :end-before: <!-- entity group image_registry end -->
```

### Project
> Entity type name: `project`

Entitlements that are granted at the `project` level might cascade to project-scoped resources (such as instances):

```{include} ../metadata.txt
    :start-after: <!-- entity group project start -->
    :end-before: <!-- entity group project end -->
```

### Storage pool
> Entity type name: `storage_pool`

```{include} ../metadata.txt
    :start-after: <!-- entity group storage_pool start -->
    :end-before: <!-- entity group storage_pool end -->
```

(permissions-project-level)=
## Project-scoped resources

Project-scoped resources are owned by projects, which, in turn, are owned by the LXD server.

### Image
> Entity type name: `image`

```{include} ../metadata.txt
    :start-after: <!-- entity group image start -->
    :end-before: <!-- entity group image end -->
```

### Image alias
> Entity type name: `image_alias`

```{include} ../metadata.txt
    :start-after: <!-- entity group image_alias start -->
    :end-before: <!-- entity group image_alias end -->
```

### Instance
> Entity type name: `instance`

```{include} ../metadata.txt
    :start-after: <!-- entity group instance start -->
    :end-before: <!-- entity group instance end -->
```

### Network
> Entity type name: `network`

```{include} ../metadata.txt
    :start-after: <!-- entity group network start -->
    :end-before: <!-- entity group network end -->
```

### Network ACL
> Entity type name: `network_acl`

```{include} ../metadata.txt
    :start-after: <!-- entity group network_acl start -->
    :end-before: <!-- entity group network_acl end -->
```

### Network zone
> Entity type name: `network_zone`

```{include} ../metadata.txt
    :start-after: <!-- entity group network_zone start -->
    :end-before: <!-- entity group network_zone end -->
```

### Placement group
> Entity type name: `placement_group`

```{include} ../metadata.txt
    :start-after: <!-- entity group placement_group start -->
    :end-before: <!-- entity group placement_group end -->
```

### Profile
> Entity type name: `profile`

```{include} ../metadata.txt
    :start-after: <!-- entity group profile start -->
    :end-before: <!-- entity group profile end -->
```

### Replicator
> Entity type name: `replicator`

```{include} ../metadata.txt
    :start-after: <!-- entity group replicator start -->
    :end-before: <!-- entity group replicator end -->
```

### Storage bucket
> Entity type name: `storage_bucket`

```{include} ../metadata.txt
    :start-after: <!-- entity group storage_bucket start -->
    :end-before: <!-- entity group storage_bucket end -->
```

### Storage volume
> Entity type name: `storage_volume`

```{include} ../metadata.txt
    :start-after: <!-- entity group storage_volume start -->
    :end-before: <!-- entity group storage_volume end -->
```
