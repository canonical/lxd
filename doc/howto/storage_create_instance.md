(howto-storage-create-instance)=
# How to create an instance in a specific storage pool

Instance storage volumes are created in the storage pool that is specified by the instance's root disk device.
This configuration is normally provided by the profile or profiles applied to the instance.
See {ref}`storage-default-pool` for detailed information.

`````{tabs}
````{group-tab} CLI
To use a different storage pool when creating or launching an instance, add the `--storage` flag.
This flag overrides the root disk device from the profile.
For example:

    lxc launch <image> <instance_name> --storage <storage_pool>
````

````{group-tab} UI

To create an instance in a specific storage pool, override the root storage during instance creation.

To do this, begin the {ref}`instance creation wizard <instances-create>`. Once the {guilabel}`Base Image` is selected, the {guilabel}`Devices` section of the left-hand sub-menu becomes available. From this section, select {guilabel}`Devices` > {guilabel}`Disk`.

```{figure} /images/instances/create_instance_form_disk_devices.png
:width: 80%
:alt: LXD Create instance form
```
In this page, in the {guilabel}`Override` column, click the Edit button to create an override.

```{figure} /images/instances/create_instance_form.png
:width: 80%
:alt: LXD Create instance disk devices form
```

From here, you can override the pool and size of the root storage by editing their respective fields.

````
`````

% Include content from [storage_move_volume.md](storage_move_volume.md)
```{include} storage_move_volume.md
    :start-after: (storage-move-instance)=
```
