---
discourse: lxc:[New&#32;disaster&#32;recovery&#32;tool](11296)
---

(disaster-recovery)=
# How to recover instances in case of disaster

```{youtube} https://youtu.be/IFOZpAxckPo?t=796
```

LXD provides a tool for disaster recovery in case the {ref}`LXD database <database>` is corrupted or otherwise lost.

The tool scans the storage pools for instances and imports the instances, custom volumes and buckets that it finds back into the database.
You need to re-create the required entities that are missing (usually pools, profiles, projects, and networks).

```{important}
This tool should be used for disaster recovery only.
Do not rely on this tool as an alternative to proper backups; you will lose data like profiles, network definitions, or server configuration.

The tool must be run interactively and cannot be used in automated scripts.
```

The tool is available through the `lxd recover` command (note the `lxd` command rather than the normal `lxc` command).

## Recovery process

When you run the tool, it scans all storage pools that still exist in the database, looking for missing volumes that can be recovered.
Any unknown storage pools (those that exist on disk but do not exist in the database) which are discovered whilst scanning existing and unknown volumes
are printed so they can be created manually using the `lxc storage create` command.

After mounting the specified storage pools (if not already mounted), the tool scans them for unknown volumes that look like they are associated with LXD.
LXD maintains a `backup.yaml` file in each instance's storage volume, which contains all necessary information to recover a given instance (including instance configuration, attached devices, storage volume, and pool configuration).
This data can be used to rebuild the instance, storage volume, attached custom volumes, and storage pool database records.
Before recovering an instance, the tool performs some consistency checks to compare what is in the `backup.yaml` file with what is actually on disk (such as matching snapshots).
If all checks out, the database records are re-created.

The tool asks you to re-create missing entities like networks.
However, the tool does not know how the instance was configured.
That means that if some configuration was specified through the `default` profile, you must also re-add the required configuration to the profile.
For example, if the `lxdbr0` bridge is used in an instance and you are prompted to re-create it, you must add it back to the `default` profile so that the recovered instance uses it.

## Example

This is how a recovery process could look.
We start by adding the `default` pool we still know about. On this pool we expect an instance `v1` which might use volumes from others unknown pools:

```{terminal}
lxc storage create default zfs source=/dev/sdb zfs.pool_name=default source.recover=true

Storage pool default created
```

```{terminal}
lxd recover

This LXD server currently has the following storage pools:
 - Pool "default" using driver "zfs"
Would you like to continue with scanning for lost volumes? (yes/no) [default=yes]:
Scanning for unknown volumes...
The following unknown volumes have been found:
 - Virtual-Machine "v1" on pool "default" in project "default" (includes 0 snapshots)
 - Volume "vol1" on pool "backup" in project "default" (includes 0 snapshots)
You are currently missing the following:
 - Pool "backup" using driver "lvm" (lvm.thinpool_name="LXDThinPool" lvm.vg_name="backup" source="backup" volatile.initial_source="/dev/sdc")
Please create those missing entries and then hit ENTER:
```

The instance `v1` was discovered successfully.
It has an additional custom volume `vol1` attached from pool `backup` which isn't yet known.
In another terminal create the missing pool after copying the pool's configuration and adding the `source.recover=true` configuration item:

```{terminal}
lxc storage create backup lvm lvm.thinpool_name="LXDThinPool" lvm.vg_name="backup" source="backup" volatile.initial_source="/dev/sdc" source.recover=true

Storage pool backup created
```

Go back to the original terminal and hit ENTER:

```{terminal}
lxd recover

...

This LXD server currently has the following storage pools:
 - Pool "backup" using driver "lvm"
 - Pool "default" using driver "zfs"
Would you like to continue with scanning for lost volumes? (yes/no) [default=yes]:
Scanning for unknown volumes...
The following unknown volumes have been found:
 - Container "u1" on pool "backup" in project "default" (includes 0 snapshots)
 - Container "u2" on pool "backup" in project "default" (includes 0 snapshots)
 - Volume "vol1" on pool "backup" in project "default" (includes 0 snapshots)
 - Virtual-Machine "v1" on pool "default" in project "default" (includes 0 snapshots)
You are currently missing the following:
 - Network "lxdbr0" in project "default"
Please create those missing entries and then hit ENTER:
```

As we are now scanning one additional pool, we were able to identify even more missing resources.
Create the missing network in another terminal:

```{terminal}
lxc network create lxdbr0

Network lxdbr0 created
```

In the original terminal hit ENTER one last time:

```{terminal}
lxd recover

...

This LXD server currently has the following storage pools:
 - Pool "backup" using driver "lvm"
 - Pool "default" using driver "zfs"
Would you like to continue with scanning for lost volumes? (yes/no) [default=yes]:
Scanning for unknown volumes...
The following unknown volumes have been found:
 - Volume "vol1" on pool "backup" in project "default" (includes 0 snapshots)
 - Container "u1" on pool "backup" in project "default" (includes 0 snapshots)
 - Container "u2" on pool "backup" in project "default" (includes 0 snapshots)
 - Virtual-Machine "v1" on pool "default" in project "default" (includes 0 snapshots)
Would you like those to be recovered? (yes/no) [default=no]: yes
Starting recovery...
```

```{terminal}
lxc list

+------+---------+------+------+-----------------+-----------+
| NAME |  STATE  | IPV4 | IPV6 |      TYPE       | SNAPSHOTS |
+------+---------+------+------+-----------------+-----------+
| u1   | STOPPED |      |      | CONTAINER       | 0         |
+------+---------+------+------+-----------------+-----------+
| u2   | STOPPED |      |      | CONTAINER       | 0         |
+------+---------+------+------+-----------------+-----------+
| v1   | STOPPED |      |      | VIRTUAL-MACHINE | 0         |
+------+---------+------+------+-----------------+-----------+
```

```{terminal}
lxc profile device add default eth0 nic network=lxdbr0 name=eth0

Device eth0 added to default
```

```{terminal}
lxc start u1
```

```{terminal}
lxc list

+------+---------+----------------------+-----------------------------------------------+-----------------+-----------+
| NAME |  STATE  |         IPV4         |                     IPV6                      |      TYPE       | SNAPSHOTS |
+------+---------+----------------------+-----------------------------------------------+-----------------+-----------+
| u1   | RUNNING | 10.178.27.235 (eth0) | fd42:f9e6:53c2:ccc5:216:3eff:fe49:e549 (eth0) | CONTAINER       | 0         |
+------+---------+----------------------+-----------------------------------------------+-----------------+-----------+
| u2   | STOPPED |                      |                                               | CONTAINER       | 0         |
+------+---------+----------------------+-----------------------------------------------+-----------------+-----------+
| v1   | STOPPED |                      |                                               | VIRTUAL-MACHINE | 0         |
+------+---------+----------------------+-----------------------------------------------+-----------------+-----------+
```
