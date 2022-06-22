# How to move or copy storage volumes

You can copy or move storage volumes from one storage pool to another, or copy or rename them within the same storage pool.

When copying or moving a volume between storage pools that use different drivers, the volume is automatically converted.

(storage-copy)=
## Copy storage volumes

Use the following command to copy a storage volume:

    lxc storage volume copy <source_pool_name>/<source_volume_name> <target_pool_name>/<target_volume_name>

Add the `--volume-only` flag to copy only the volume and skip any snapshots that the volume might have.
If the volume already exists in the target location, use the `--refresh` flag to update the copy.

Specify the same pool as the source and target pool to copy the volume within the same storage pool.
You must specify different volume names for source and target in this case.

When copying from one storage pool to another, you can either use the same name for both volumes or rename the new volume.

## Move or rename storage volumes

Before you can move or rename a storage volume, all instances that use it must be [stopped](https://linuxcontainers.org/lxd/getting-started-cli/#start-and-stop-an-instance).

Use the following command to move or rename a storage volume:

    lxc storage volume move <source_pool_name>/<source_volume_name> <target_pool_name>/<target_volume_name>

Specify the same pool as the source and target pool to rename the volume while keeping it in the same storage pool.
You must specify different volume names for source and target in this case.

When moving from one storage pool to another, you can either use the same name for both volumes or rename the new volume.

## Copy or move between cluster members

For most storage drivers (except for `ceph` and `ceph-fs`), storage volumes exist only on the cluster member for which they were created.

To copy or move a storage volume from one cluster member to another, add the `--target` and `--destination-target` flags to specify the source cluster member and the target cluster member, respectively.

## Copy or move between projects

Add the `--target-project` to copy or move a storage volume to a different project.

## Copy or move between LXD servers

You can copy or move storage volumes between different LXD servers by specifying the remote for each pool:

    lxc storage volume copy <source_remote>:<source_pool_name>/<source_volume_name> <target_remote>:<target_pool_name>/<target_volume_name>
    lxc storage volume move <source_remote>:<source_pool_name>/<source_volume_name> <target_remote>:<target_pool_name>/<target_volume_name>

You can add the `--mode` flag to choose a transfer mode, depending on your network setup:

`pull` (default)
: Instruct the target server to pull the respective storage volume.

`push`
: Push the storage volume from the source server to the target server.

`relay`
: Pull the storage volume from the source server to the local client, and then push it to the target server.
