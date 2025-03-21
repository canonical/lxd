(cluster-link)=
# How to link a cluster

Cluster links allow you to establish a trusted connection between two independent LXD clusters. This enables cross-cluster authentication for delegated operations through identity groups.

For conceptual information about cluster links, see {ref}`cluster-links`.

## Requirements

- Two running LXD clusters (the source cluster and the target cluster)
- Network connectivity between the clusters
- Administrative access to both clusters

## Understanding cluster links

A cluster link consists of:

- A name to identify the linked cluster
- The reachable network addresses of the linked cluster
- An optional description
- Optional identity groups for authorization

When a link is established, each cluster stores the TLS certificate of the other cluster for authentication.

## Link creation process

The process of linking two clusters involves the following steps:

1. Generate a cluster link token on the source cluster (Cluster A)
1. Use that token to establish the link on the target cluster (Cluster B)
1. Complete the link verification automatically

### Step 1: Initiate the link on the source cluster

On your source cluster (Cluster A), run:

```
lxc cluster link add <target-cluster-name> [--address <addr1>,<addr2>] [--group <group1>,<group2>] [--description <description>]
```

Where:

- `<target-cluster-name>` is a name you assign to identify the target cluster (Cluster B)
- `--address` (optional) specifies IP addresses where the target cluster can be reached
- `--group` (optional) specifies identity groups to assign to the target cluster
- `--description` (optional) provides a description for the link

For example:

```
lxc cluster link add backup-cluster --address 10.0.0.1:8443,10.0.0.2:8443 --group backups --description "Backup LXD cluster"
```

This command will:

- Create a pending identity and cluster link in Cluster A's database
- Add the pending identity to any specified identity groups
- Return a token that you'll use in the next step

### Step 2: Complete the link on the target cluster

Take the token generated in Step 1 and use it on the target cluster (Cluster B):

```
lxc cluster link add <source-cluster-name> <token> [--address <addr1>,<addr2>] [--group <group1>,<group2>] [--description <description>]
```

Where:

- `<source-cluster-name>` is a name you assign to identify the source cluster (Cluster A)
- `<token>` is the token received from Cluster A
- `--address` (optional) overrides the addresses in the token with custom addresses
- `--group` (optional) specifies identity groups to assign to the source cluster
- `--description` (optional) provides a description for the link

For example:

```
lxc cluster link add main-cluster ABCDEF123456 --group prod-clusters --description "Main production cluster"
```

### Verification process

When completing the link in Step 2:

1. The target cluster (Cluster B) validates that:
   - The addresses from the token and/or the command line are reachable
   - All addresses return the same cluster certificate
   - The certificate fingerprint matches the one in the token

1. The target cluster (Cluster B) then sends its own certificate to the source cluster (Cluster A)

1. The source cluster (Cluster A) validates:
   - The target cluster's certificate
   - That the addresses are reachable
   - All addresses return the same certificate

1. When validation is successful:
   - The pending link on the source cluster is activated
   - A new link is created on the target cluster
   - Both clusters store each other's certificates for authentication

## Managing cluster links

### Listing cluster links

To view all cluster links:

```
lxc cluster link list [<remote>:]
```

Example output:

```
+----------------+------------------------+----------------------+
|      NAME      |       ADDRESSES        |     DESCRIPTION      |
+----------------+------------------------+----------------------+
| backup-cluster | 10.0.0.1:8443,         | Backup LXD cluster   |
|                | 10.0.0.2:8443          |                      |
+----------------+------------------------+----------------------+
```

### Viewing link details

To view the details of a specific cluster link:

```
lxc cluster link show [<remote>:]<link-name>
```

### Editing a cluster link

You can edit the description or addresses of a cluster link:

```
lxc cluster link edit [<remote>:]<link-name>
```

This opens an editor with the current configuration. Alternatively, you can use stdin:

```
cat updated-config.yaml | lxc cluster link edit [<remote>:]<link-name>
```

### Removing a cluster link

To remove a cluster link:

```
lxc cluster link remove [<remote>:]<link-name>
```

## Using cluster links

Once clusters are linked and appropriate identity groups are configured, you can:

1. Assign permissions to identity groups to control what operations the linked cluster can perform
1. Use delegated operations between clusters

For example, to allow a linked cluster to retrieve instance information but not modify it, you could create a permission that grants read-only access to instances and assign it to the identity group associated with the cluster link.

## Troubleshooting

If you encounter issues when creating a cluster link:

- Ensure network connectivity between the clusters
- Verify that the addresses specified are correct and reachable
- Check that the certificates are valid and not expired
- Review your firewall settings to ensure port 8443 (or your custom port) is open

If a link fails during creation, you can remove the pending link and try again:

```
lxc cluster link remove <link-name>
```

## Security considerations

- Cluster links establish trust between clusters, so only link clusters that should have access to each other
- Use identity groups to limit what linked clusters can do
