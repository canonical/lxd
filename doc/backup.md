# Backing up a LXD server
## What to backup
When planning to backup a LXD server, consider all the different objects
that are stored/managed by LXD:

 - Containers (database records and filesystems)
 - Images (database records, image files and filesystems)
 - Networks (database records and state files)
 - Profiles (database records)
 - Storage volumes (database records and filesystems)

Only backing up the database or only backing up the container filesystem
will not get you a fully functional backup.

In some disaster recovery scenarios, that may be reasonable but if your
goal is to get back online quickly, consider all the different pieces of
LXD you're using.

## Full backup
A full backup would include the entirety of `/var/lib/lxd` or
`/var/snap/lxd/common/lxd` for snap users.

You will also need to appropriately backup any external storage that you
made LXD use, this can be LVM volume groups, ZFS zpools or any other
resource which isn't directly self-contained to LXD.

Restoring involves stopping LXD on the target server, wiping the lxd
directory, restoring the backup and any external dependency it requires.

Then start LXD again and check that everything works fine.

## Secondary backup LXD server
LXD supports copying and moving containers and storage volumes between two hosts.

So with a spare server, you can copy your containers and storage volumes
to that secondary server every so often, allowing it to act as either an
offline spare or just as a storage server that you can copy your
containers back from if needed.

## Container backups
The `lxc export` command can be used to export containers to a backup tarball.
Those tarballs will include all snapshots by default and an "optimized"
tarball can be obtained if you know that you'll be restoring on a LXD
server using the same storage pool backend.

Those tarballs can be saved any way you want on any filesystem you wan
and can be imported back into LXD using the `lxc import` command.

## Disaster recovery
Additionally, LXD maintains a `backup.yaml` file in each container's storage
volume. This file contains all necessary information to recover a given
container, such as container configuration, attached devices and storage.

This file can be processed by the `lxd import` command, not to
be confused with `lxc import`.

To use the disaster recovery mechanism, you must mount the container's
storage to its expected location, usually under
`storage-pools/NAME-OF-POOL/containers/NAME-OF-CONTAINER`.

Depending on your storage backend you will also need to do the same for
any snapshot you want to restore (needed for `dir` and `btrfs`).

Once everything is mounted where it should be, you can now run `lxd import NAME-OF-CONTAINER`.

If any matching database entry for resources declared in `backup.yaml` is found
during import, the command will refuse to restore the container.  This can be
overridden by passing `--force`.
