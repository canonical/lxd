---
myst:
  html_meta:
    description: How to securely decommission a LXD server, cluster member, or cluster.
---

(howto-decommission)=
# How to securely decommission a LXD deployment

Follow these steps to securely decommission your LXD deployment on a standalone server, cluster member, or cluster.

```{important}
This process will erase all data associated with your LXD deployment.
Make copies of any data that you need to preserve before proceeding.
Refer to {ref}`instances-backup` and {ref}`howto-storage-backup-volume` for relevant details.
```

To decommission a standalone server, proceed directly to {ref}`howto-decommission-revoke-remote`.
To decommission a single member of a cluster, proceed to {ref}`howto-decommission-remove-offline-member` or {ref}`howto-decommission-remove-online-member`, depending on the member's status.
To decommission an entire cluster, first {ref}`delete replicators and cluster links <howto-decommission-delete-replicators-links>`.

(howto-decommission-delete-replicators-links)=
## Delete replicators and cluster links

```{note}
Only delete replicators and cluster links if you are decommissioning an **entire** cluster.
```

Replicators are scoped by {ref}`project <projects>` and must be deleted by project.
First, list all projects:

```bash
lxc project list
```

Next, list all replicators for each project, then delete each replicator:

```bash
lxc replicator list --project <project_name>
lxc replicator delete <replicator_name> --project <project_name>
```

Finally, list cluster links, then delete each one:

```bash
lxc cluster link list
lxc cluster link delete <cluster_link_name>
```

(howto-decommission-remove-offline-member)=
## Remove offline cluster members

To decommission an entire cluster or a single offline member, {ref}`remove offline cluster members <cluster-manage-offline-members>` with the `--force` flag:

```bash
lxc cluster remove --force <member_name>
```

````{important}
If you are only decommissioning the offline member, {ref}`update the cluster certificate <cluster-manage-update-certificate>` on the cluster.
After removing the offline member, run this command on any member of the cluster remaining in production:

```bash
lxc cluster update-certificate <cert.crt> <cert.key>
```
````

Then, to decommission the cluster member, proceed to {ref}`howto-decommission-remove-lxd`.
To decommission the remaining cluster, proceed to {ref}`howto-decommission-revoke-remote` (you will remove online cluster members after you delete data).

(howto-decommission-remove-online-member)=
## Remove an online cluster member

To decommission a single, online cluster member, first {ref}`evacuate instances <cluster-evacuate>` from the cluster member to other members:

```bash
lxc cluster evacuate <member_name>
```

Instances that are not suitable for migration may remain on the cluster member.
Verify that no instances remain on the cluster member:

```bash
lxc list location=<member_name> --all-projects
```

If instances remain on the cluster member, {ref}`migrate those instances <howto-instances-migrate>` to another cluster member:

```bash
lxc move <instance_name> --target <target_member_name>
```

{ref}`Remove the member <cluster-manage-delete-members>` from the cluster:

```bash
lxc cluster remove <member_name>
```

Then, {ref}`update the cluster certificate <cluster-manage-update-certificate>` by running this command on any member of the cluster remaining in production:

```bash
lxc cluster update-certificate <cert.crt> <cert.key>
```

Now proceed to {ref}`howto-decommission-remove-lxd` to decommission the removed member.

(howto-decommission-revoke-remote)=
## Revoke remote access

```{note}
To decommission an entire cluster, run the commands in this section on any cluster member.
```

List all identities that have access to LXD, then delete each one:

```bash
lxc auth identity list
lxc auth identity delete <type>/<name_or_identifier>
```

To decommission a deployment {ref}`configured for single sign-on with OIDC <howto-oidc>`, remove the corresponding profile from your OIDC identity provider.

(howto-decommission-delete-data)=
## Delete data

```{important}
Data deleted by LXD physically remains on disks and can be recovered by users with access to the disks.
To prevent unauthorized data recovery, you must {ref}`destroy and sanitize your data <howto-decommission-destroy-data>`.

To decommission a single cluster member, run these commands **after** removing the member from the cluster.
Do **not** run these commands on any member of a cluster that will remain in production.
```

```{note}
To decommission an entire cluster, run the commands in this section on any cluster member.
```

### List projects

Instances, profiles, custom volumes, and buckets are scoped by {ref}`project <projects>`.
For deployments with more than one project, you must repeat some steps for **each** project, using the `--project` flag.
You do not need to use the `--project` flag to decommission deployments with only one project.

View all projects:

```bash
lxc project list
```

````{note}
You can also delete a project (except the `default` project) and all of its project-level entities with:

```bash
lxc project delete <project_name> --force
```
````

### Stop and delete instances

For each project, stop all instances:

```bash
lxc stop --all --project <project_name>
```

Next, list the instances for each project, then delete each instance:

```bash
lxc list --project <project_name>
lxc delete <instance_name> --project <project_name>
```

````{note}
If you are unable to stop or delete an instance, use the `--force` flag:

```bash
lxc stop --force <instance_name> --project <project_name>
lxc delete --force <instance_name> --project <project_name>
```
````

### Delete profiles

For each project, list all profiles, then delete every profile but the `default` profile:

```bash
lxc profile list --project <project_name>
lxc profile delete <profile_name> --project <project_name>
```

```{note}
The `default` profile cannot be deleted.
```

### Delete custom volumes and buckets

You must specify storage pools to delete {ref}`custom volumes <storage-volume-types>` or {ref}`buckets <storage-buckets>`.
First, list all storage pools:

```bash
lxc storage list
```

```{note}
You do not need to specify a project.
This command lists all storage pools across projects.
```

Next, list the custom volumes on each storage pool.
Use the `--all-projects` flag to view all custom volumes across projects:

```bash
lxc storage volume list <pool_name> type=custom --all-projects
```

Then, for {ref}`Ceph Object <storage-cephobject>` pools only, list the buckets:

```bash
lxc storage bucket list <pool_name> --all-projects
```

Use the `PROJECT` column in the output to identify the project associated with each custom volume or bucket.

Finally, delete all custom volumes and buckets, specifying the storage pool and project:

```bash
lxc storage volume delete <pool_name> <volume_name> --project <project_name>
lxc storage bucket delete <pool_name> <bucket_name> --project <project_name>
```

### Delete storage pools

Storage pools cannot be deleted if they are used by an instance, profile, custom volume, or bucket.
The `default` profile cannot be deleted; therefore, the storage pool used by the `default` profile cannot be deleted.
To identify this storage pool, view information about the `default` profile and find the pool listed under `devices` > `root` > `pool`:

```bash
lxc profile show default
```

Next, list all storage pools, then delete every pool but the one used by the `default` profile.

```bash
lxc storage list
lxc storage delete <pool_name>
```

```{note}
You do not need to specify a project when running these commands.
```

### Delete monitoring data

Delete data from any external systems that you used to {ref}`monitor events <howto-security-events>` or {ref}`monitor metrics <metrics>`, such as [Loki](https://grafana.com/oss/loki/), [Prometheus](https://prometheus.io/), or [Grafana](https://grafana.com/).
Refer to the documentation for those systems for details.

(howto-decommission-remove-remaining-members)=
## Remove remaining cluster members

To decommission an entire cluster, list all cluster members, then remove each one:

```bash
lxc cluster list
lxc cluster remove <member_name>
```

```{note}
Iterate over every cluster member name except the name of the member on which you are running the commands.

Offline cluster members should have been removed before revoking remote access.
See {ref}`howto-decommission-remove-offline-member`.
```

(howto-decommission-remove-lxd)=
## Remove the LXD snap

```{important}
Run these commands on **every** machine that you decommission.

Removing LXD **does not** erase dedicated disks/partitions, {ref}`ZFS pools (zpools) <storage-zfs>`, {ref}`LVM volume groups <storage-lvm>`, or {ref}`remote storage <storage-drivers-remote>`.
To securely decommission LXD, you must {ref}`destroy and sanitize your data <howto-decommission-destroy-data>`.
```

Remove the LXD snap.
Use the `--purge` flag, or a snapshot of your data will be preserved:

```bash
sudo snap remove lxd --purge
```

Verify that the snap and associated data were removed.
The following commands should report that LXD is not installed and that the `/var/snap/lxd/` directory does not exist:

```bash
snap list lxd
ls /var/snap/lxd/
```

```{note}
If you followed a different method to {ref}`install LXD <installing>`, use your package manager to remove LXD.
Then, delete the data in `/var/lib/lxd/`.
```

(howto-decommission-destroy-data)=
## Destroy and sanitize data

Data deleted by LXD remains readable and can be recovered by users with access to disks used in your deployment.
To prevent unauthorized recovery, you must physically overwrite the data.

Follow your data destruction policy to securely erase and destroy disks used by LXD, as well as disks on machines used to monitor LXD events or metrics.
Consult storage providers for details about how to securely sanitize data on {ref}`remote storage <storage-drivers-remote>`.
For deployments {ref}`configured for single sign-on with OIDC <howto-oidc>`, consult your OIDC identity provider for the steps to remove any associated data.

```{important}
Sanitized data is irreversibly destroyed and cannot be recovered.
```
