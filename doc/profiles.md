# Profiles

## Introduction

Profiles can store any configuration that an instance can (key/value or devices)
and any number of profiles can be applied to an instance.

Profiles are applied in the order they are specified so the last profile to
specify a specific key wins.

In any case, instance-specific configuration always overrides that coming from
the profiles.

## Default profile

If not present, LXD will create a `default` profile.
The `default` profile cannot be renamed or removed.
The `default` profile is set for any new instance created which doesn't
specify a different profiles list.

## Configuration

As profiles aren't specific to containers or virtual machines, they may
contain configuration and devices that are valid for either type.

This differs from the behavior when applying those configurations/devices
directly to an instance where its type is then taken into consideration
and keys that aren't allowed result in an error.

See [instance configuration](instances.md) for valid configuration options.
