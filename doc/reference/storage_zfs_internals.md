---
myst:
  html_meta:
    description: Internal implementation details of the LXD ZFS storage driver, including image variant datasets and soft deletion.
---

(storage-zfs-internals)=
# ZFS storage driver internals

This page describes implementation details of the ZFS storage driver that are not required for day-to-day use but are useful for understanding its behavior or for debugging.

## Image variant datasets

When LXD unpacks an image on a ZFS pool, it stores the result as an *image variant* dataset.
The dataset naming convention is:

- `<pool>/images/<fingerprint>` : dataset variant (when {config:option}`storage-zfs-volume-conf:zfs.block_mode` is `false`)
- `<pool>/images/<fingerprint>_<filesystem>` : block-backed variant, where `<filesystem>` is `ext4`, `btrfs`, or `xfs` (when {config:option}`storage-zfs-volume-conf:zfs.block_mode` is `true`)

Each variant dataset has a `@readonly` ZFS snapshot.
When a new instance is created from the image, LXD clones this `@readonly` snapshot rather than unpacking the image again, making instance creation nearly instantaneous.

## Soft deletion

When an image is deleted but one or more instances are still cloned from a variant dataset, LXD cannot immediately destroy the dataset (ZFS does not allow destroying a dataset that has dependent clones).
Instead, the variant is renamed to `<pool>/deleted/images/<fingerprint>` (or `<pool>/deleted/images/<fingerprint>_<filesystem>` for block-backed variants). This is referred to as *soft deletion*.

The soft-deleted dataset persists until the last instance that depends on it is removed.
At that point LXD destroys the dataset permanently.
