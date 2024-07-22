(permissions-reference)=
# Permissions

When managing user access via {ref}`fine-grained-authorization`, you add identities to groups and then grant entitlements against specific LXD API resources to these groups.

Each LXD API resource has a particular entity type, and each entity type has a set of entitlements that can be granted against API resources of that type.

Below is a description of each entity type, and a list of entitlements that can be granted against entities of that type.

## Server
> Entity type name: `server`

The `server` entity type is the top-level entity type for the LXD system.
Entitlements that are granted at this level might cascade to projects and other resources:

```{include} ../metadata.txt
    :start-after: <!-- entity group server start -->
    :end-before: <!-- entity group server end -->
```

## Project
> Entity type name: `project`

Entitlements that are granted at the `project` level might cascade to project specific resources (such as instances):

```{include} ../metadata.txt
    :start-after: <!-- entity group project start -->
    :end-before: <!-- entity group project end -->
```

## Storage pool
> Entity type name: `storage_pool`

```{include} ../metadata.txt
    :start-after: <!-- entity group storage_pool start -->
    :end-before: <!-- entity group storage_pool end -->
```

## Identity
> Entity type name: `identity`

```{include} ../metadata.txt
    :start-after: <!-- entity group identity start -->
    :end-before: <!-- entity group identity end -->
```

## Group
> Entity type name: `group`

```{include} ../metadata.txt
    :start-after: <!-- entity group group start -->
    :end-before: <!-- entity group group end -->
```

## Identity provider group
> Entity type name: `identity_provider_group`

```{include} ../metadata.txt
    :start-after: <!-- entity group identity_provider_group start -->
    :end-before: <!-- entity group identity_provider_group end -->
```

## Certificate
> Entity type name: `certificate`

```{include} ../metadata.txt
    :start-after: <!-- entity group certificate start -->
    :end-before: <!-- entity group certificate end -->
```

## Instance
> Entity type name: `instance`

```{include} ../metadata.txt
    :start-after: <!-- entity group instance start -->
    :end-before: <!-- entity group instance end -->
```

## Image
> Entity type name: `image`

```{include} ../metadata.txt
    :start-after: <!-- entity group image start -->
    :end-before: <!-- entity group image end -->
```

## Image alias
> Entity type name: `image_alias`

```{include} ../metadata.txt
    :start-after: <!-- entity group image_alias start -->
    :end-before: <!-- entity group image_alias end -->
```

## Network
> Entity type name: `network`

```{include} ../metadata.txt
    :start-after: <!-- entity group network start -->
    :end-before: <!-- entity group network end -->
```

## Network ACL
> Entity type name: `network_acl`

```{include} ../metadata.txt
    :start-after: <!-- entity group network_acl start -->
    :end-before: <!-- entity group network_acl end -->
```

## Network zone
> Entity type name: `network_zone`

```{include} ../metadata.txt
    :start-after: <!-- entity group network_zone start -->
    :end-before: <!-- entity group network_zone end -->
```

## Profile
> Entity type name: `profile`

```{include} ../metadata.txt
    :start-after: <!-- entity group profile start -->
    :end-before: <!-- entity group profile end -->
```

## Storage volume
> Entity type name: `storage_volume`

```{include} ../metadata.txt
    :start-after: <!-- entity group storage_volume start -->
    :end-before: <!-- entity group storage_volume end -->
```

## Storage bucket
> Entity type name: `storage_bucket`

```{include} ../metadata.txt
    :start-after: <!-- entity group storage_bucket start -->
    :end-before: <!-- entity group storage_bucket end -->
```
