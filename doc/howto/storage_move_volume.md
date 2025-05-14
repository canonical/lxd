---
discourse: lxc:[Migrate&#32;from&#32;one&#32;storage&#32;pool&#32;to&#32;another](10877)
---

(howto-storage-move-volume)=
# How to move or copy storage volumes

You can {ref}`copy <storage-copy-volume>` or {ref}`move <storage-move-volume>` custom storage volumes from one storage pool to another, or copy or rename them within the same storage pool.

To move instance storage volumes from one storage pool to another, {ref}`move the corresponding instance <storage-move-instance>` to another pool.

When copying or moving a volume between storage pools that use different drivers, the volume is automatically converted.

(storage-copy-volume)=
## Copy custom storage volumes

`````{tabs}
````{group-tab} CLI
Use the following command to copy a custom storage volume:

    lxc storage volume copy <source_pool_name>/<source_volume_name> <target_pool_name>/<target_volume_name>

Add the `--volume-only` flag to copy only the volume and skip any snapshots that the volume might have.
If the volume already exists in the target location, use the `--refresh` flag to update the copy (see {ref}`storage-optimized-volume-transfer` for the benefits).

Specify the same pool as the source and target pool to copy the volume within the same storage pool.
You must specify different volume names for source and target in this case.

When copying from one storage pool to another, you can either use the same name for both volumes or rename the new volume.

````
````{group-tab} UI
To copy a custom storage volume, navigate to the {guilabel}`Overview` page of the storage volume you wish to copy, and click {guilabel}`Copy`.

```{figure} /images/storage/storage_volumes_overview.png
:width: 80%
:alt: LXD Custom Storage Volume overview page
```

In the {guilabel}`Copy volume` modal, you can define a new name for the copied volume as well as a number of other attributes.

```{figure} /images/storage/storage_volumes_copy_modal.png
:width: 60%
:alt: LXD Custom Storage Volume copy volume modal
```

````
`````

(storage-move-volume)=
## Move or rename custom storage volumes

`````{tabs}
````{group-tab} CLI

Before you can move or rename a custom storage volume, all instances that use it must be {ref}`stopped <instances-manage-stop>`.

Use the following command to move or rename a storage volume:

    lxc storage volume move <source_pool_name>/<source_volume_name> <target_pool_name>/<target_volume_name>

Specify the same pool as the source and target pool to rename the volume while keeping it in the same storage pool.
You must specify different volume names for source and target in this case.

When moving from one storage pool to another, you can either use the same name for both volumes or rename the new volume.

````
````{group-tab} UI

To rename a custom storage volume, navigate to its {guilabel}`Overview` page and select its name in the header to edit it.

```{figure} /images/storage/storage_volumes_rename.png
:width: 60%
:alt: LXD Rename Custom Storage Volume
```

````
`````

## Copy or migrate between cluster members

`````{tabs}
````{group-tab} CLI

For most storage drivers (except for `ceph` and `ceph-fs`), storage volumes exist only on the cluster member for which they were created.

To copy or migrate a custom storage volume from one cluster member to another, add the `--target` and `--destination-target` flags to specify the source cluster member and the target cluster member, respectively.

````
````{group-tab} UI

You can use the LXD UI to copy storage volumes between cluster members, but not to migrate them.

To copy a storage volume, navigate to the {guilabel}`Overview` page of the storage volume within a clustered environment, then click {guilabel}`Copy`.

In the {guilabel}`Copy volume` modal, select the target cluster member from the {guilabel}`Cluster member` dropdown.

````
`````

## Copy or move between projects

`````{tabs}
````{group-tab} CLI

Add the `--target-project` to copy or move a custom storage volume to a different project.

````
````{group-tab} UI

To copy a storage volume between projects, navigate to the {guilabel}`Overview` page of the storage volume, then click {guilabel}`Copy`.

In the {guilabel}`Copy volume` modal, select the target from the {guilabel}`Target project` dropdown.
````
`````

## Copy or migrate between LXD servers

You can copy a custom volume from one LXD server to another, or migrate it (move it between servers), by specifying the remote for each pool:

    lxc storage volume copy <source_remote>:<source_pool_name>/<source_volume_name> <target_remote>:<target_pool_name>/<target_volume_name>
    lxc storage volume move <source_remote>:<source_pool_name>/<source_volume_name> <target_remote>:<target_pool_name>/<target_volume_name>

You can add the `--mode` flag to choose a transfer mode, depending on your network setup:

`pull` (default)
: Instruct the target server to pull the respective storage volume.

`push`
: Push the storage volume from the source server to the target server.

`relay`
: Pull the storage volume from the source server to the local client, and then push it to the target server.

If the volume already exists in the target location, use the `--refresh` flag to update the copy (see {ref}`storage-optimized-volume-transfer` for the benefits).

(storage-move-instance)=
## Move instance storage volumes to another pool

To move an instance storage volume to another storage pool, {ref}`stop the instance <instances-manage-stop>` that contains the storage volume you want to move.

`````{tabs}
````{group-tab} CLI
Use the following command to move the instance to a different pool:

    lxc move <instance_name> --storage <target_pool_name>
````

````{group-tab} UI

Navigate to the overview page of the selected instance, and click on the {guilabel}`Migrate` button in the top right corner.

```{figure} /images/instances/instance_overview_page.png
:width: 80%
:alt: LXD Instance overview page
```

Within the move modal, click {guilabel}`Move instance root storage to a different pool` to view available storage pools to move to.

```{figure} /images/instances/move_instance_modal.png
:width: 80%
:alt: LXD Instance root storage move method modal
```
Click {guilabel}`Select` in the row of the target storage pool for the move.

```{figure} /images/instances/move_instance_modal_2.png
:width: 80%
:alt: LXD Instance root storage move pool selection modal
```

On the resulting confirmation modal, click {guilabel}`Move` to finalize the process.

```{figure} /images/instances/move_confirmation_modal.png
:width: 80%
:alt: LXD Instance root storage confirmation modal
```

````
`````
