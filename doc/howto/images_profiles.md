(images-profiles)=
# How to associate profiles with an image

You can associate one or more profiles with a specific image.
Instances that are created from the image will then automatically use the associated profiles in the order they were specified.

To associate a list of profiles with an image, add the profiles to the image configuration in the `profiles` section (see {ref}`images-manage-edit`).

`````{tabs}
````{group-tab} CLI
Use the [`lxc image edit`](lxc_image_edit.md) command to edit the `profiles` section:

```yaml
profiles:
- default
```
````
````{group-tab} API
To update the full image properties, including the `profiles` section, send a PUT request with the full image data:

    lxc query --request PUT /1.0/images/<fingerprint> --data '<image_configuration>'

See [`PUT /1.0/images/{fingerprint}`](swagger:/images/image_put) for more information.
````
`````

Most provided images come with a profile list that includes only the `default` profile.
To prevent any profile (including the `default` profile) from being associated with an image, pass an empty list.

```{note}
Passing an empty list is different than passing `nil`.
If you pass `nil` as the profile list, only the `default` profile is associated with the image.
```

You can override the associated profiles for an image when creating an instance by adding the `--profile` or the `--no-profiles` flag to the launch or init command (when using the CLI), or by specifying a list of profiles in the request data (when using the API).
