(devices-none)=
# Type: `none`

```{youtube} https://www.youtube.com/watch?v=6NCLnd5_guQ
```

```{note}
The `none` device type is supported for both containers and VMs.
```

A `none` device doesn't have any properties and doesn't create anything inside the instance.

Its only purpose is to stop inheriting devices that come from profiles.
To do so, add a device with the same name as the one that you do not want to inherit, but with the device type `none`.

You can add this device either in a profile that is applied after the profile that contains the original device, or directly on the instance.
