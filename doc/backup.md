---
discourse: 11296
---

(backups)=
# Backing up a LXD server

## What to back up

When planning to back up a LXD server, consider all the different entities
that are stored/managed by LXD:

- Instances (database records and file systems)
- Images (database records, image files and file systems)
- Networks (database records and state files)
- Profiles (database records)
- Storage volumes (database records and file systems)

Only backing up the database or only backing up the instances will not
get you a fully functional backup.

In some disaster recovery scenarios, that may be reasonable but if your
goal is to get back online quickly, consider all the different pieces of
LXD you're using.

## Full backup

A full backup would include the entirety of `/var/lib/lxd` or `/var/snap/lxd/common/lxd` for snap users.

You will also need to appropriately back up any external storage that you
made LXD use, this can be LVM volume groups, ZFS zpools or any other
resource which isn't directly self-contained to LXD.

Restoring involves stopping LXD on the target server, wiping the `lxd`
directory, restoring the backup and any external dependency it requires.

If not using the snap package and your source system has a `/etc/subuid`
and `/etc/subgid` file, restoring those or at least the entries inside
them for both the `lxd` and `root` user is also a good idea
(avoids needless shifting of container file systems).

Then start LXD again and check that everything works fine.

## Secondary backup LXD server

LXD supports copying and moving instances and storage volumes between two hosts.

So with a spare server, you can copy your instances and storage volumes
to that secondary server every so often, allowing it to act as either an
offline spare or just as a storage server that you can copy your
instances back from if needed.

## Instance backups

The `lxc export` command can be used to export instances to a backup tarball.
Those tarballs will include all snapshots by default and an "optimized"
tarball can be obtained if you know that you'll be restoring on a LXD
server using the same storage pool backend.

You can use any compressor installed on the server using the `--compression` flag.
There is no validation on the LXD side, any command that is available
to LXD and supports `-c` for stdout should work.

Those tarballs can be saved any way you want on any file system you want
and can be imported back into LXD using the `lxc import` command.

## Disaster recovery

LXD provides the `lxd recover` command (note the `lxd` command rather than the normal `lxc` command).
This is an interactive CLI tool that will attempt to scan all storage pools that exist in the database looking for
missing volumes that can be recovered. It also provides the ability for the user to specify the details of any
unknown storage pools (those that exist on disk but do not exist in the database) and it will attempt to scan those
too.

Because LXD maintains a `backup.yaml` file in each instance's storage volume which contains all necessary
information to recover a given instance (including instance configuration, attached devices, storage volume and
pool configuration) it can be used to rebuild the instance, storage volume and storage pool database records.

The `lxd recover` tool will attempt to mount the storage pool (if not already mounted) and scan it for unknown
volumes that look like they are associated with LXD. For each instance volume LXD will attempt to mount it and
access the `backup.yaml` file. From there it will perform some consistency checks to compare what is in the
`backup.yaml` file with what is actually on disk (such as matching snapshots) and if all checks out then the
database records are recreated.

If the storage pool database record also needs to be created then it will prefer to use an instance `backup.yaml`
file as the basis of its configuration, rather than what the user provided during the discovery phase, however if not
available then it will fallback to restoring the pool's database record with what was provided by the user.
