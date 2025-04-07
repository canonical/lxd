---
myst:
  html_meta:
    description: Create cluster links between LXD clusters using trust tokens and mutual TLS.
---

(howto-cluster-links-create)=
# How to create cluster links

{ref}`Cluster links <exp-cluster-links>` can connect separate LXD clusters by establishing a trust relationship using mutual TLS with certificates, ensuring secure communication.

(howto-cluster-links-auth)=
## Prepare authentication

Before creating cluster links, set up proper authentication groups and {ref}`manage-permissions`:

```bash
lxc auth group create <group-name>
lxc auth group permission add <group-name> <entity-type> <entitlement>
```

The example below shows how to create an authentication group for each cluster called `link` with the `admin` entitlement on the `server` entity type:

```{code-block} bash
:caption: Example: Cluster A
lxc auth group create link
lxc auth group permission add link server admin
```

```{code-block} bash
:caption: Example: Cluster B
lxc auth group create link
lxc auth group permission add link server admin
```

Adjust the permissions according to your security requirements. {ref}`Fine-grained permissions <fine-grained-authorization>` can be applied to control what operations each cluster can perform on the other.

For example, you can create a more restricted group for backup operations only:

```bash
lxc auth group create backup
lxc auth group permission add backup instance can_manage_backups
```

## Create a cluster link

To create a new cluster link between two clusters (Cluster A and Cluster B), you must create the link on both sides. Follow these steps:

1. On Cluster A, create a new cluster link to Cluster B and receive a trust token:

   ```bash
   lxc cluster link create <name-of-link-to-cluster-b> --auth-group <auth-group-name>
   ```

   This command:
   - Creates a pending identity for Cluster B under the link name you provided.
   - Assigns this identity to the specified authentication group.
   - Returns a trust token.

   Copy the trust token. You'll need it for the next step.

   Example:

   ```bash
   lxc cluster link create cluster_b --auth-group clusters
   ```

1. On Cluster B, create the corresponding cluster link using the trust token from Cluster A:

   ```bash
   lxc cluster link create <name-of-link-to-cluster-a> --token <token-from-A> --auth-group <auth-group-name>
   ```

   This command:
   - Verifies the token's fingerprint against Cluster A's certificate.
   - Creates an identity for Cluster A under the name you provided and assigns it to the specified authentication group.
   - Activates the pending link with Cluster A by sending Cluster B's certificate.
   - Establishes bidirectional trust between the clusters.

   Example:

   ```bash
   lxc cluster link create cluster_a --token <token-from-A> --auth-group clusters
   ```

(howto-cluster-links-identities)=
## View the underlying identities

When you create a cluster link, LXD automatically creates an identity for authentication. You can view this identity with:

```bash
lxc auth identity show tls/<cluster-link-name>
```

The output shows the identity of your cluster link, with the type `Cluster link certificate`.
