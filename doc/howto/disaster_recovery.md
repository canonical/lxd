---
discourse: lxc:[New&#32;disaster&#32;recovery&#32;tool](11296)
myst:
  html_meta:
    description: How to use the interactive lxd recover tool to recover LXD database records by scanning storage pools to identify missing volumes and other resources.
---

(disaster-recovery)=
# How to recover LXD database records after a disaster

```{youtube} https://www.youtube.com/watch?v=vJhTjhQYKJs&t=466s
:title: LXD backup and disaster recovery
```

LXD provides an interactive tool for performing disaster recovery when the {ref}`LXD database <database>` is corrupted or lost but the storage pools remain.

You can access the tool through the `lxd recover` command (note that this is a `lxd` command, not a `lxc` command).
When you run this command, the tool scans known storage pools and identifies volumes (instances and custom volumes) missing from the LXD database.
In the process, the tool may also identify other missing entities, such as projects, networks, and additional storage pools.
The tool can automatically re-create database records for the discovered volumes, but you must re-create other required entities separately.

```{important}
Do not rely on this tool as an alternative to proper backups.
The recovery tool cannot recover a complete LXD deployment.
For example, the recovery tool cannot recover data such as profile configuration, network definitions, or server configuration.

Only use this tool for disaster recovery. You must run the tool interactively; do not use the tool in automated scripts.
```

## Recovery process

When you run `lxd recover`, the recovery tool scans all storage pools that exist in the database and identifies missing volumes that can be recovered.
The tool also scans volumes on the known storage pools and, in the process, may discover additional storage pools that exist on disk but are missing from the LXD database.
In such cases, the tool prints information about the storage pools so that you can re-create their database records manually.
Concrete recovery examples for each storage driver can be found in {ref}`howto-storage-pools-recover`.
The tool then mounts any unmounted storage pools and continues scanning for volumes that may be associated with LXD.

Through this scan, the recovery tool can identify some custom volumes by name.
Some {ref}`remote storage drivers <storage-drivers-remote>`, however, such as the {ref}`PowerFlex <storage-powerflex>`, {ref}`PowerStore <storage-powerstore>`, and {ref}`Pure <storage-pure>` drivers, use transformed volume names, and the recovery tool is unable to discover these volumes from their name alone.
Instead, these volumes can only be discovered if they are attached to an instance.

LXD maintains a `backup.yaml` file in each instance's storage volume, which contains all necessary information to recover a given instance.
The recovery tool compares the `backup.yaml` file with what is actually on disk (such as matching snapshots) and, if this consistency check passes, re-creates the database records.
The tool can also use the `backup.yaml` file to gather information about profiles, storage pools, and attached devices (such as storage volumes with transformed names).
Based on this information, the tool will prompt you to re-create missing entities, but it will not display information about how those entities were configured, unless they are storage pools.
For example, if an instance used a bridge network attached to a profile, the tool will notify you that the two entities are missing from the database, but it will not direct you to attach the network to the profile.

## How to use the recovery tool

```{note}
The `lxd recover` tool requires a working installation of LXD.
If your LXD database is corrupted, you may need to remove and then reinstall the LXD snap before proceeding with the steps described below.
```

The following sections illustrate how to use the recovery tool through an example scenario.
The example is based on a LXD deployment with the following entities:

- Two storage pools: `default` (a {ref}`ZFS <storage-zfs>` pool) and `backup` (a {ref}`LVM <storage-lvm>` pool)
- One project: `default`
- One virtual machine instance: `v1`, on the `default` pool
- Two container instances: `u1` and `u2`, both on the `backup` pool
- One custom volume: `vol1`, attached to `v1` and stored on the `default` pool
- One network: `lxdbr0`, attached to the `default` profile
- One profile: `default`

For the purpose of this example scenario, assume that you only know about the `default` storage pool and the `v1` virtual machine when you begin the recovery process.

### Re-create known storage pools

The recovery tool begins by scanning storage pools with records in the LXD database.
As a result, the database must already contain a record of at least one storage pool.

You can use the `lxc storage create` command with configuration key `source.recover=true` to re-create a database record for a storage pool that exists on disk but is missing from the LXD database.
You must also provide any configuration options that were originally set to non-default values.

In this example scenario, you only know about the `default` storage pool and its original configuration.
You can re-create the `default` pool by running the following command, with `source.recover=true`:

```{terminal}
lxc storage create default zfs source=/dev/sdb zfs.pool_name=default source.recover=true

Storage pool default created
```

### Begin the recovery process

Now that you have re-created the `default` storage pool, you can begin the recovery process by running `lxd recover`.
When you run the command, the recovery tool lists the currently available storage pool, `default`, and asks for confirmation before proceeding.
Press the `ENTER` key to begin a scan of the known pool.
In this example scenario, you expect the tool to discover the `v1` virtual machine that you already know about.

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

As expected, the recovery tool successfully discovered `v1` on the `default` pool.
Through `v1`, the recovery tool also discovered unknown resources: the custom volume (`vol1`) attached to `v1` and the volume's storage pool (`backup`).

### Re-create additional resources

The recovery tool can re-create unknown instances and custom volumes, such as `v1` and `vol1`, automatically, but you must manually create all other resources.

In this example, you need to re-create the `backup` pool before proceeding.
As with the `default` storage pool, you must use the configuration key `source.recover=true`, along with the original configuration options.
To assist with this, the recovery tool printed the pool configurations that it discovered.

You can re-create a database record for the storage pool in another terminal:

```{terminal}
lxc storage create backup lvm lvm.thinpool_name="LXDThinPool" lvm.vg_name="backup" source="backup" volatile.initial_source="/dev/sdc" source.recover=true

Storage pool backup created
```

Back in the original terminal, press the `ENTER` key, then enter `yes` to resume the scan.
The tool again scans the known pools, which now include both `default` and `backup`.

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

In scanning the new pool, the recovery tool identified several more missing resources.
The recovery tool can re-create containers `u1` and `u2` automatically, but you must re-create the missing network separately.

Create the missing network in another terminal:

```{terminal}
lxc network create lxdbr0

Network lxdbr0 created
```

Then return to the original terminal, press the `ENTER` key, then enter `yes` to resume the scan:

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

Now that you have re-created the missing resources (`backup` and `lxdbr0`), the recovery tool can recover the missing volumes (instances and custom volumes).

### Restore resource configurations

Although the recovery tool identifies some missing resources and re-creates records for missing instances and custom volumes, it cannot recover complete information about how resources were configured.
For example, the tool cannot recover network definitions or profile configurations, and you must instead retain a separate record of these configurations and restore them manually.
To simplify this process, you can {ref}`keep a backup of the LXD database <backup-database>`.

In this example scenario, the `lxdbr0` network was originally attached to the `default` profile, and you must restore this configuration separately:

```{terminal}
lxc profile device add default eth0 nic network=lxdbr0 name=eth0

Device eth0 added to default
```

### Inspect your deployment

Once the recovery process is complete, you can inspect the deployment's instances:

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

You can also start an instance:

```{terminal}
lxc start u1
```

```{terminal}
lxc list

+------+---------+----------------------+-----------------------------------------------+-----------------+-----------+
| NAME |  STATE  |         IPV4         |                     IPV6                      |      TYPE       | SNAPSHOTS |
+------+---------+----------------------+-----------------------------------------------+-----------------+-----------+
| u1   | RUNNING | 192.0.2.2 (eth0)     | 2001:db8:cff3:5089:216:3eff:fef0:549f (eth0)  | CONTAINER       | 0         |
+------+---------+----------------------+-----------------------------------------------+-----------------+-----------+
| u2   | STOPPED |                      |                                               | CONTAINER       | 0         |
+------+---------+----------------------+-----------------------------------------------+-----------------+-----------+
| v1   | STOPPED |                      |                                               | VIRTUAL-MACHINE | 0         |
+------+---------+----------------------+-----------------------------------------------+-----------------+-----------+
```
