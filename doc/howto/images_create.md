---
relatedlinks: '[How&#32;to&#32;install&#32;a&#32;Windows&#32;11&#32;VM&#32;using&#32;LXD](https://ubuntu.com/tutorials/how-to-install-a-windows-11-vm-using-lxd)'
---

(images-create)=
# How to create images

If you want to create and share your own images, you can do this either based on an existing instance or snapshot or by building your own image from scratch.

(images-create-publish)=
## Publish an image from an instance or snapshot

If you want to be able to use an instance or an instance snapshot as the base for new instances, you should create and publish an image from it.

When publishing an image from an instance, make sure that the instance is stopped.

````{tabs}
```{group-tab} CLI
To publish an image from an instance, enter the following command:

    lxc publish <instance_name> [<remote>:]

To publish an image from a snapshot, enter the following command:

    lxc publish <instance_name>/<snapshot_name> [<remote>:]

In both cases, you can specify an alias for the new image with the `--alias` flag, set an expiration date with `--expire` and make the image publicly available with `--public`.
If an image with the same name already exists, add the `--reuse` flag to overwrite it.
See [`lxc publish --help`](lxc_publish.md) for a full list of available flags.

```
```{group-tab} API
To publish an image from an instance or a snapshot, send a POST request with the suitable source type to the `/1.0/images` endpoint.

To publish an image from an instance:

    lxc query --request POST /1.0/images --data '{
      "source": {
        "name": "<instance_name>",
        "type": "instance"
      }
    }'

To publish an image from a snapshot:

    lxc query --request POST /1.0/images --data '{
      "source": {
        "name": "<instance_name>/<snapshot_name>",
        "type": "snapshot"
      }
    }'

In both cases, you can include additional configuration (for example, you can include aliases, set a custom expiration date, or make the image publicly available).
For example:

    lxc query --request POST /1.0/images --data '{
      "aliases": [ { "name": "<alias>" } ],
      "expires_at": "2025-03-23T20:00:00-04:00",
      "public": true,
      "source": {
        "name": "<instance_name>",
        "type": "instance"
      }
    }'

See [`POST /1.0/images`](swagger:/images/images_post) for more information.
```
```{group-tab} UI
The UI does not currently support publishing an image from an instance, but you can publish from a snapshot.

To do so, go to the instance detail page and switch to the {guilabel}`Snapshots` tab.
Then click the {guilabel}`Create image` button ({{create_image_button}}) and optionally enter an alias for the new image.
You can also choose whether the image should be publicly available.

Publishing the image might take a few minutes.
You can check the status under {guilabel}`Operations`.
```
````

The publishing process can take quite a while because it generates a tarball from the instance or snapshot and then compresses it.
As this can be particularly I/O and CPU intensive, publish operations are serialized by LXD.

### Prepare the instance for publishing

Before you publish an image from an instance, clean up all data that should not be included in the image.
Usually, this includes the following data:

- Instance metadata (use [`lxc config metadata`](lxc_config_metadata.md) or [`PATCH /1.0/instances/{name}/metadata`](swagger:/instances/instance_metadata_patch)/[`PUT /1.0/instances/{name}/metadata`](swagger:/instances/instance_metadata_put) to edit)
- File templates (use [`lxc config template`](lxc_config_template.md) or [`POST /1.0/instances/{name}/metadata/templates`](swagger:/instances/instance_metadata_templates_post) to edit)
- Instance-specific data inside the instance itself (for example, host SSH keys and `dbus/systemd machine-id`)

(images-create-build)=
## Build an image

For building your own images, you can use [LXD image builder](https://github.com/canonical/lxd-imagebuilder).

See the [LXD image builder documentation](https://canonical-lxd-imagebuilder.readthedocs-hosted.com/en/latest/) for instructions for installing and using the tool.

(images-repack-windows)=
### Repack a Windows image

You can run Windows VMs in LXD.
To do so, you must repack the Windows ISO with LXD image builder.

See the {doc}`LXD image builder tutorial <imagebuilder:tutorials/use>` for instructions, or [How to install a Windows 11 VM using LXD](https://ubuntu.com/tutorials/how-to-install-a-windows-11-vm-using-lxd) for a full walk-through.
