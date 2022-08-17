# How to resize a storage volume

If you need more storage in a volume, you can increase the size of your storage volume.
In some cases, it is also possible to reduce the size of a storage volume.

To resize a storage volume, set its size configuration:

    lxc storage volume set <pool_name> <volume_name> size <new_size>

```{important}
- Growing a storage volume usually works (if the storage pool has sufficient storage).
- Shrinking a storage volume is only possible for storage volumes with content type `filesystem`.
  It is not guaranteed to work though, because you cannot shrink storage below its current used size.
- Shrinking a storage volume with content type `block` is not possible.

```
