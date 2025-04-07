(howto-cluster-links-create)=
# How to create cluster links

Cluster links can connect separate LXD clusters by establishing a trust relationship using mutual TLS with certificates, ensuring secure communication.

(howto-cluster-links-auth)=
## Prepare authentication

Before creating cluster links, set up proper authentication groups and {ref}`manage-permissions`:

For example, create an authentication group called link with admin permissions for both clusters:

```{code-block} bash
:caption: Cluster A
lxc auth group create link
lxc auth group permission add link server admin
```

```{code-block} bash
:caption: Cluster B
lxc auth group create link
lxc auth group permission add link server admin
```

Adjust the permissions according to your security requirements. {ref}`Fine-grained permissions <fine-grained-authorization>` can be applied to control what operations each cluster can perform on the other.

For example, to create a more restricted group for backup operations only:

```{code-block} bash
lxc auth group create backup
lxc auth group permission add backup instance can_manage_backups
```

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
