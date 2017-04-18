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
Additionally, LXD maintains a `backup.yaml` file in the containers storage
volume. This file contains all necessary information to recover a given
container. The tool `lxd import` is designed to process this file and to
restore containers from it.
This recovery mechanism is mostly meant for emergency recoveries and will try
to re-create container and storage database entries from a backup of the
storage pool configuration. This requires that the corresponding storage volume
for the container exists and is accessible. For example, if the container's
storage volume got unmounted the user is required to remount it manually.
Note that if any existing database entry is found then `lxd import` will refuse
to restore the container unless the `--force` flag is passed which will cause
LXD to delete and replace any currently existing db entries.
