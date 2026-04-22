---
discourse: lxc:[Share&#32;folders&#32;and&#32;volumes&#32;between&#32;host&#32;and&#32;containers](7735)
myst:
  html_meta:
    description: An index of how-to guides for LXD storage operations, including managing pools, volumes, and buckets, and using storage with Kubernetes.
---

(storage)=
# Storage

These how-to guides cover common operations related to storage in LXD.

## Create and manage storage

LXD storage pools contain instance volumes and custom volumes, as well as buckets accessible via the S3 protocol.

```{toctree}
:titlesonly:

Manage pools </howto/storage_pools>
Manage volumes </howto/storage_volumes>
Manage buckets </howto/storage_buckets>
```

## Extend storage use

Instance volumes can be created directly in a specific storage pool. Custom volumes can also be moved, copied, and backed up.

```{toctree}
:titlesonly:
Create or move an instance in a pool </howto/storage_create_instance>
Back up a custom volume </howto/storage_backup_volume>
Move or copy a custom volume </howto/storage_move_volume>
```

## Use storage with Kubernetes

The LXD CSI driver integrates LXD storage backends with Kubernetes.

```{toctree}
:titlesonly:
Use the LXD CSI driver with Kubernetes </howto/storage_csi>
```

## Related topics

{{storage_exp}}

{{storage_ref}}
