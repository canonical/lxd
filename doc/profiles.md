# Profiles
Profiles can store any configuration that a container can (key/value or
devices) and any number of profiles can be applied to a container.

Profiles are applied in the order they are specified so the last profile to
specify a specific key wins.

In any case, resource-specific configuration always overrides that coming from
the profiles.

If not present, LXD will create a `default` profile.

The `default` profile cannot be renamed or removed.

The `default` profile is set for any new container created which doesn't
specify a different profiles list.


See [container configuration](containers.md) for valid configuration options.
