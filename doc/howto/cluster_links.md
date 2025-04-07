(howto-cluster-links)=
# How to set up cluster links

(howto-cluster-links-prereqs)=
## Prerequisites

Before setting up {ref}`exp-cluster-links`:

1. Both clusters should be initialized.
1. You need sufficient permissions on both clusters to establish the links.

(howto-cluster-links-auth)=
## Prepare authentication

Before creating cluster links, set up proper authentication groups and {ref}`manage-permissions`:

```{code-block} bash
:caption: Cluster A
lxc auth group create clusters
lxc auth group permission add clusters server admin
```

```{code-block} bash
:caption: Cluster B
lxc auth group create clusters
lxc auth group permission add clusters server admin
```

Adjust the permissions according to your security requirements. {ref}`Fine-grained permissions <fine-grained-authorization>` can be applied to control what operations each cluster can perform on the other.

For example, to create a more restricted group for backup operations only:

```{code-block} bash
lxc auth group create backup
lxc auth group permission add backup instance can_manage_backups
```

(howto-cluster-links-create)=
## Create a cluster link

To create a new cluster link between two clusters (Cluster A and Cluster B), follow these steps:

1. On Cluster A, create a new cluster link and get a trust token:

   ```bash
   lxc cluster link create cluster_b --auth-group clusters
   ```

   This command:
   - Creates a pending identity for Cluster B
   - Assigns this identity to the specified authentication group
   - Returns a trust token that you'll need for the next step

1. On Cluster B, create a cluster link using the trust token from Cluster A:

   ```bash
   lxc cluster link create cluster_a <token from A> --auth-group clusters
   ```

   This command:
   - Verifies the token's fingerprint against Cluster A's certificate
   - Creates an identity for Cluster A and assigns it to the specified authentication group
   - Activates the pending link with Cluster A by sending Cluster B's certificate
   - Establishes bidirectional trust between the clusters

(howto-cluster-links-identities)=
## Understanding the underlying identities

When you create a cluster link, LXD automatically creates an identity for authentication. You can view this identity with:

```bash
lxc auth identity show tls/<cluster_link_name>
```

The output shows the identity of your cluster link, with the type `Cluster link certificate`.

(howto-cluster-links-permissions)=
## Manage cluster link permissions

To modify the permissions of a cluster link, add its identity to authentication groups.

(howto-cluster-links-view)=
## View cluster links

To view all cluster links for your cluster, use:

```bash
lxc cluster link list
```

This shows all links along with their addresses.

To view cluster link configuration for a specific cluster, use:

```bash
lxc cluster link show <name>
```

To view detailed information about a specific cluster link:

```bash
lxc cluster link info <name>
```

(howto-cluster-links-modify)=
## Configure a cluster link

See {ref}`cluster-link-config` for more details on cluster link configuration options.

To configure a cluster link, use

```bash
lxc cluster link edit <name>
```

Alternatively, use the `set` command to modify specific properties or configuration options:

```bash
lxc cluster link set <name> <key> <value>
```

For example, to update the description property:

```bash
lxc cluster link set cluster_b -p description "Backup cluster in data center 2"
```

(howto-cluster-links-delete)=
## Delete a cluster link

To delete a cluster link, use:

```bash
lxc cluster link delete <name>
```

This removes the established trust and deletes the associated identity on the local cluster. To fully disconnect the clusters, run the command on both clusters:

```{code-block} bash
:caption: Cluster A
lxc cluster link remove cluster_b
```

```{code-block} bash
:caption: Cluster B
lxc cluster link remove cluster_a
```
