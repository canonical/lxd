---
discourse: lxc:[Cluster&#32;links](placeholder)
---

(howto-cluster-links)=
# How to set up cluster links

{ref}`cluster-links` allow you to connect separate LXD clusters to enable scaling beyond the ~50 member limit of a single cluster. This provides various benefits such as extended capacity, failover capabilities, disaster recovery options, and geographically distributed backups.

## Prerequisites

Before setting up cluster links:

1. Both clusters should be initialized
1. You need sufficient permissions on both clusters to establish the links

## Prepare authentication

Before creating cluster links, it's recommended to set up proper authentication groups and permissions to control access levels:

```bash
# On Cluster A
lxc auth group create clusters
lxc auth group permission add clusters server admin

# On Cluster B
lxc auth group create clusters
lxc auth group permission add clusters server admin
```

You can adjust the permissions according to your security requirements. Fine-grained permissions can be applied to control what operations each cluster can perform on the other.

For example, to create a more restricted group for backup operations only:

```bash
lxc auth group create backup
lxc auth group permission add backup instance can_manage_backups
lxc auth group permission add backup instance can_manage_backups
```

## Create a cluster link

To create a new cluster link between two clusters (Cluster A and Cluster B), follow these steps on both clusters:

1. On Cluster A, create a new cluster link and get a trust token:

   ```bash
   lxc cluster link add cluster_b --auth-group clusters
   ```

   This command:
   - Creates a pending identity for Cluster B
   - Assigns this identity to the specified authentication group
   - Returns a trust token that you'll need for the next step

1. On Cluster B, create a cluster link using the trust token from Cluster A:

   ```bash
   lxc cluster link add cluster_a <token from A> --auth-group clusters
   ```

   This:
   - Verifies the token's fingerprint against Cluster A's certificate
   - Creates an identity for Cluster A and assigns it to the specified authentication group
   - Activates the pending link on Cluster A by sending Cluster B's certificate
   - Establishes bidirectional trust between the clusters

## Understanding the underlying identities

When you create a cluster link, LXD automatically creates an identity for authentication. You can view this identity with:

```bash
lxc auth identity list
```

The output will show an identity of type `Cluster link certificate` with the name of your cluster link.

## Manage cluster link permissions

You can modify the permissions of a cluster link by adding its identity to authentication groups:

## View cluster links

To view all cluster links for your cluster, use the following command:

```bash
lxc cluster link list
```

This shows all links along with their addresses.

To view detailed information about a specific cluster link:

```bash
lxc cluster link show <cluster-name>
```

## Modify a cluster link

You can modify a cluster link's description or addresses using the `edit` command:

```bash
lxc cluster link edit <cluster-name>
```

Alternatively, you can use the `set` command to modify specific properties:

```bash
lxc cluster link set <cluster-name> <key> <value>
```

For example, to update the description:

```bash
lxc cluster link set cluster_b description "Backup cluster in data center 2"
```

## Remove a cluster link

To remove a cluster link, use the following command:

```bash
lxc cluster link remove <cluster-name>
```

This removes the established trust between the clusters and deletes the associated identity. To fully disconnect the clusters, you should perform this operation on both clusters:

```bash
# On Cluster A
lxc cluster link remove cluster_b

# On Cluster B
lxc cluster link remove cluster_a
```
