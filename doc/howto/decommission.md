---
myst:
  html_meta:
    description: How to securely decommission a LXD server, cluster member, or cluster.
---

(howto-decommission)=
# How to securely decommission a LXD deployment

```{important}
This process will erase all data associated with your LXD deployment.
Make copies of any data that you need to preserve before proceeding.
Refer to {ref}`instances-backup` and {ref}`howto-storage-backup-volume` for relevant details.
```

This guide walks you through the steps to decommission a LXD cluster. You can also follow this guide to decommission a standalone server, though the steps regarding cluster members will not apply in such cases.

If you only need to decommission a single member of a cluster, first {ref}`remove that member from the cluster <cluster-manage-delete-members>`.
After removing the member, {ref}`update the certificate <cluster-manage-update-certificate>` on the cluster remaining in production.
Then, return to this guide, skip ahead to {ref}`howto-decommission-remove-lxd`, and follow the instructions in the sections that follow.

Unless otherwise noted, you can run the commands in this guide on any online cluster member.

(howto-decommission-remove-offline-member)=
## Remove offline cluster members

Run this command with the `--force` flag to {ref}`remove any offline cluster members <cluster-manage-offline-members>` (you will remove online cluster members later in the process):

```bash
lxc cluster remove --force <offline_member_name>
```

(howto-decommission-revoke-remote)=
## Revoke remote access

List all identities that have access to LXD, then delete each one:

```bash
lxc auth identity list
lxc auth identity delete <authentication_method>/<name_or_identifier>
```

To decommission a deployment {ref}`configured for single sign-on with OIDC <howto-oidc>`, remove the corresponding profile from your OIDC identity provider.

(howto-decommission-list-projects)=
## List projects

Replicators, instances, profiles, custom volumes, and buckets are scoped by {ref}`project <projects>`.
By default, LXD commands only affect project-scoped entities on the currently active project; you can specify a different project by using the `--project` flag.

If your deployment has more than one project, this means that you must repeat project-scoped commands with the `--project` flag to delete entities across projects.
Note that you do not need to use the `--project` flag to delete entities on the currently active project (for example, `default`).

Run this command to get a list of all projects:

```bash
lxc project list
```

````{note}
You can also delete a project (except the `default` project) and all of its project-level entities with:

```bash
lxc project delete <project_name> --force
```
````

(howto-decommission-delete-replicators-links)=
## Delete replicators and cluster links

For each project, list all replicators, then delete each one:

```bash
lxc replicator list --project <project_name>
lxc replicator delete <replicator_name> --project <project_name>
```

Likewise, list all cluster links, then delete each one (cluster links are not scoped by project, so you do not need to use the `--project` flag):

```bash
lxc cluster link list
lxc cluster link delete <cluster_link_name>
```

To fully revoke the trust relationship established by cluster links, delete any corresponding cluster links or identities on the other clusters.
See the {ref}`instructions for deleting cluster links <howto-cluster-links-delete>` for details.

(howto-decommission-delete-data)=
## Delete data

```{important}
Data deleted by LXD physically remains on disks and can be recovered by users with access to the disks.
To prevent unauthorized data recovery, you must {ref}`destroy and sanitize your data <howto-decommission-destroy-data>`.
```

(howto-decommission-delete-instances)=
### Stop and delete instances

For each project, stop all instances:

```bash
lxc stop --all --project <project_name>
```

Next, for each project, list all instances, then delete each one:

```bash
lxc list --project <project_name>
lxc delete <instance_name> --project <project_name>
```

If you are unable to stop or delete an instance, use the `--force` flag:

```bash
lxc stop --force <instance_name> --project <project_name>
lxc delete --force <instance_name> --project <project_name>
```

(howto-decommission-delete-profiles)=
### Delete profiles

For each project, list all profiles:

```bash
lxc profile list --project <project_name>
```

Each project has a `default` profile that cannot be deleted. Delete all other profiles:

```
lxc profile delete <profile_name> --project <project_name>
```

(howto-decommission-remove-disk-devices)=
### Remove disk devices from `default` profiles

You cannot delete a storage pool used by an instance, profile, or custom volume.
You must, therefore, remove any disk devices used by `default` profiles in order to delete any storage pools or custom volumes referenced by those devices.

For example, if you configured a new storage pool during the {ref}`interactive initialization process <initialize>` with `lxd init`, then the `default` profile of the `default` project will have a disk device named `root` that references a storage pool.

Remove this device with:

```bash
lxc profile device remove default root --project default
```

To check for additional disk devices, view information about the `default` profile of each project:

```bash
lxc profile show default --project <project_name>
```

Remove any remaining disk devices that reference storage pools:

```bash
lxc profile device remove default <device_name> --project <project_name>
```

(howto-decommission-delete-volumes-buckets)=
### Delete custom volumes and buckets

You must specify storage pools to delete {ref}`custom volumes <storage-volume-types>` or {ref}`buckets <storage-buckets>`.
First, list all storage pools across projects:

```bash
lxc storage list
```

Next, list the custom volumes on each storage pool.
Use the `--all-projects` flag to view all custom volumes across projects:

```bash
lxc storage volume list <pool_name> type=custom --all-projects
```

Then, for {ref}`Ceph Object <storage-cephobject>` pools only, list all buckets:

```bash
lxc storage bucket list <pool_name> --all-projects
```

Use the `PROJECT` column in the output to identify the project associated with each custom volume or bucket.

Finally, delete all custom volumes and buckets, specifying both the storage pool and the project:

```bash
lxc storage volume delete <pool_name> <volume_name> --project <project_name>
lxc storage bucket delete <pool_name> <bucket_name> --project <project_name>
```

(howto-delete-storage-pools)=
### Delete storage pools

List all storage pools, then delete each one (storage pools are not scoped by project, so you do not need to use the `--project` flag):

```bash
lxc storage list
lxc storage delete <pool_name>
```

(howto-delete-monitoring-data)=
### Delete monitoring data

Delete data from any external systems that you used to {ref}`monitor events <howto-security-events>` or {ref}`monitor metrics <metrics>`, such as [Loki](https://grafana.com/oss/loki/), [Prometheus](https://prometheus.io/), or [Grafana](https://grafana.com/).
Refer to the documentation for those systems for details.

(howto-decommission-remove-remaining-members)=
## Remove remaining cluster members

After deleting data, you can remove the online cluster members from the cluster.
List all cluster members, then remove each one:

```bash
lxc cluster list
lxc cluster remove <member_name>
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
Likewise, if you used {ref}`ACME services to issue server certificates <authentication-server-certificate>`, refer to the service provider for the steps to remove any associated data.

```{important}
Sanitized data is irreversibly destroyed and cannot be recovered.
```
