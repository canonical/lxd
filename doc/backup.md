# LXD Backup Strategies

To backup a LXD instance different strategies are available.

## Full backup
This requires that the whole `/var/lib/lxd` folder will be backuped up.
Additionally, it is necessary to backup all storage pools as well.

In order to restore the LXD instance the old `/var/lib/lxd` folder needs to be
removed and replaced with the `/var/lib/lxd` snapshot. All storage pools
need to be restored as well.

## Secondary LXD
This requires a second LXD instance to be setup and reachable from the LXD
instance that is to be backed up. Then, all containers can be copied to the
secondary LXD instance for backup.

## Container backup and restore
Additionally, LXD maintains a `backup.yaml` file in each container's storage
volume. This file contains all necessary information to recover a given
container, such as container configuration, attached devices and storage.
This file can be processed by the `lxd import` command.

Running 

```bash
lxd import <container-name>
```

will restore the specified container from its `backup.yaml` file.  This
recovery mechanism is mostly meant for emergency recoveries and will try to
re-create container and storage database entries from a backup of the storage
pool configuration.

Note that the corresponding storage volume for the container must exist and be
accessible before the container can be imported.  For example, if the
container's storage volume got unmounted the user is required to remount it
manually.

If any matching database entry for resources declared in `backup.yaml` is found
during import, the command will refuse to restore the container.  This can be
overridden running 

```bash
lxd import --force <container-name>
```

which causes LXD to delete and replace any currently existing db entries.
