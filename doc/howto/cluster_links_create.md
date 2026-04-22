---
myst:
  html_meta:
    description: Create bidirectional and unidirectional cluster links between LXD clusters.
---

(howto-cluster-links-create)=
# How to create cluster links

{ref}`Cluster links <exp-cluster-links>` connect separate LXD clusters. There are three link types — bidirectional, unidirectional (authenticated), and unidirectional unauthenticated — each with a different creation flow.

(howto-cluster-links-auth)=
## Prepare authentication

Before creating bidirectional or authenticated unidirectional cluster links, set up proper authentication groups and {ref}`manage-permissions`:

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

(howto-cluster-links-create-bidirectional)=
## Create a bidirectional cluster link

To create a bidirectional cluster link between two clusters (Cluster A and Cluster B), you must create the link on both sides. Follow these steps:

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

(howto-cluster-links-create-unidirectional)=
## Create an authenticated unidirectional cluster link

An authenticated unidirectional link lets Cluster A access Cluster B's resources. B creates an identity for A, but A does not create an identity for B — B cannot initiate requests to A via a cluster link.

Follow these steps:

1. On Cluster B (the target), issue a pending identity token:

   ```bash
   lxc auth identity create cluster-link/<name-for-cluster-a> --auth-group <auth-group-name>
   ```

   This command creates a pending `Cluster link certificate` identity on B and returns a trust token.

   Example:

   ```bash
   lxc auth identity create cluster-link/cluster_a --auth-group clusters
   ```

1. On Cluster A (the initiator), create the cluster link using the token from Cluster B:

   ```bash
   lxc cluster link create <name-for-cluster-b> --token <token-from-B> --unidirectional
   ```

   This command:
   - Pins Cluster B's certificate on Cluster A.
   - Calls back to Cluster B to activate B's pending identity for A.
   - Stores B's addresses in `volatile.addresses` so A can reach B.

   Example:

   ```bash
   lxc cluster link create cluster_b --token <token-from-B> --unidirectional
   ```

After these steps, Cluster A has a link with `type: unidirectional` and no associated identity. Cluster B has an active `Cluster link certificate` identity for A and a corresponding link row.

(howto-cluster-links-create-unauthenticated)=
## Create an unauthenticated unidirectional cluster link

An unauthenticated unidirectional link lets Cluster A connect to Cluster B without presenting a client certificate. B remains completely unaware of the link. Use this type for anonymous or public access to B.

On Cluster A, run:

```bash
lxc cluster link create <name-for-cluster-b> --unauthenticated --remote-address <cluster-b-address>
```

The CLI fetches Cluster B's certificate and displays its fingerprint:

```
Certificate fingerprint: <fingerprint>
ok (y/n/[fingerprint])?
```

Confirm by typing `y` or the full fingerprint. The link is then stored locally on A with `type: unidirectional-unauthenticated`.

Example:

```bash
lxc cluster link create cluster_b --unauthenticated --remote-address 10.0.0.1:8443
```

```{admonition} No identity is created
:class: note

For unauthenticated unidirectional links, no identity is created on either cluster. Cluster B is not contacted during link creation.
```

(howto-cluster-links-identities)=
## View the underlying identities

For bidirectional and authenticated unidirectional links, LXD automatically creates an identity for authentication. You can view this identity with:

```bash
lxc auth identity show tls/<cluster-link-name>
```

The output shows the identity of your cluster link, with the type `Cluster link certificate`.

## Next steps

- {ref}`howto-replicators-setup` — set up replicators to sync instances across this link for active-passive disaster recovery.
- {ref}`howto-cluster-links-manage` — view, configure, and delete existing cluster links.
