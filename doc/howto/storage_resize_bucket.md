# How to resize a storage bucket

By default storage buckets do not have a quota applied.

To set a storage bucket quota, set its size configuration:

    lxc storage bucket set <pool_name> <bucket_name> size <new_size>

```{important}
- Growing a storage bucket usually works (if the storage pool has sufficient storage).
- You cannot shrink storage bucket below its current used size.

```
