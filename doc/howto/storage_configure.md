# How to configure storage settings

```{note}
General keys are top-level. Driver specific keys are namespaced by driver name.
Volume keys apply to any volume created in the pool unless the value is
overridden on a per-volume basis.
```

Storage pool configuration keys can be set using the lxc tool with:

```bash
lxc storage set [<remote>:]<pool> <key> <value>
```

Storage volume configuration keys can be set using the lxc tool with:

```bash
lxc storage volume set [<remote>:]<pool> <volume> <key> <value>
```

To set default volume configurations for a storage pool, set a storage pool configuration with a volume prefix i.e. `volume.<VOLUME_CONFIGURATION>=<VALUE>`.
For an example, to set the default volume size of a pool with the lxc tool, use:
```bash
lxc storage set [<remote>:]<pool> volume.size <value>
```
