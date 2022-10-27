(devices-none)=
# Type: `none`

Supported instance types: container, VM

A none type device doesn't have any property and doesn't create anything inside the instance.

It's only purpose it to stop inheritance of devices coming from profiles.

To do so, just add a none type device with the same name of the one you wish to skip inheriting.
It can be added in a profile being applied after the profile it originated from or directly on the instance.
