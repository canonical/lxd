---
myst:
  html_meta:
    description: View, configure, and delete LXD cluster links and manage their permissions.
---

(howto-cluster-links-manage)=
# How to manage cluster links

(howto-cluster-links-view)=
## View cluster links

To list all cluster links (that you have permission to see), run:

````{tabs}
```{group-tab} CLI

    lxc cluster link list

```
```{group-tab} API
To list all cluster links (that you have permission to see), send the following request:

    lxc query --request GET /1.0/cluster/links

To display detailed information about each cluster link, use {ref}`rest-api-recursion`:

    lxc query --request GET /1.0/cluster/links?recursion=1

See [`GET /1.0/cluster/links`](swagger:/cluster-links/cluster_links_get) and  [`GET /1.0/cluster/links?recursion=1`](swagger:/cluster-links/cluster_links_get_recursion1) for more information.
```
````

To view the full configuration of a specific cluster link, run:

````{tabs}
```{group-tab} CLI

    lxc cluster link show <name>

```
```{group-tab} API

    lxc query --request GET /1.0/cluster/links/<name>

See [`GET /1.0/cluster/links/{name}`](swagger:/cluster-links/{name}/cluster_link_get) for more information.
```
````

To view detailed information about the state of a specific cluster link, run:

````{tabs}
```{group-tab} CLI

    lxc cluster link info <name>

```
```{group-tab} API

    lxc query --request GET /1.0/cluster/links/<name>/state

See [`GET /1.0/cluster/links/{name}/state`](swagger:/cluster-links/{name}/state/cluster_link_state_get) for more information.
```
````

(howto-cluster-links-permissions)=
## Manage cluster link permissions

To modify the permissions of a cluster link, add its identity to authentication groups. See {ref}`manage-permissions` for more information.

For example, you can create an authentication group with server viewer permissions and add the cluster link identity to it:

```bash
lxc auth group create viewers
lxc auth group permission add viewers server viewer
lxc auth identity group add tls/<cluster-link-name> viewers
```

Alternatively, you can specify an authentication group when creating a cluster link, which will automatically assign the cluster link identity to that group.

Example:

```bash
lxc cluster link create cluster_b --auth-group server-admins
```

(howto-cluster-links-configure)=
## Configure a cluster link

See {ref}`ref-cluster-link-config` for more details on cluster link configuration options.

There are multiple ways to update the configuration for a cluster link.

You can edit the entire configuration at once:

````{tabs}
```{group-tab} CLI
To edit a cluster link in your default text editor, enter the following command:

    lxc cluster link edit <name>

```
```{group-tab} API
To edit a cluster link, send the following request:

    lxc query --request PUT /1.0/cluster/links/<name> --data "<link_configuration>"

See [`PUT /1.0/cluster/links/{name}`](swagger:/cluster-links/{name}/cluster_link_put) for more information.
```
````

You can update a single property for a cluster link:

````{tabs}
```{group-tab} CLI
Use the `set` command with the `--property` flag:

    lxc cluster link set <cluster-link-name> --property <key>=<value>

For example, to update the `description` property:

    lxc cluster link set cluster_b --property description="Backup cluster in data center 2"

```
```{group-tab} API
To modify a specific property, send the following request:

    lxc query --request PATCH /1.0/cluster/links/<name> --data '{"<key>": "<value>"}'

Example:

    lxc query --request PATCH /1.0/cluster/links/cluster_b --data '{"description": "Backup cluster in data center B"}'

See [`PATCH /1.0/cluster/links/{name}`](swagger:/cluster-links/{name}/cluster_link_patch) for more information.
```
````

Cluster links have the following properties:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group cluster-link-properties start -->
    :end-before: <!-- config group cluster-link-properties end -->
```

You can also update a single configuration option for a cluster link. Run:

````{tabs}
```{group-tab} CLI

    lxc cluster link set <name> <key>=<value>

```
```{group-tab} API

    lxc query --request PATCH /1.0/cluster/links/<name> --data '{"config": <config>}'

See [`PATCH /1.0/cluster/links/{name}`](swagger:/cluster-links/{name}/cluster_link_patch) for more information.
```
````

(howto-cluster-links-delete)=
## Delete a cluster link

To delete a cluster link, run:

````{tabs}
```{group-tab} CLI

    lxc cluster link delete <name>

```
```{group-tab} API

    lxc query --request DELETE /1.0/cluster/links/<name>

See [`DELETE /1.0/cluster-links/{name}`](swagger:/cluster-links/{name}/cluster_link_delete) for more information.
```
````

```{admonition} To fully disconnect the cluster link on both sides
:class: note

To fully disconnect the clusters, run the command on both clusters.

Deleting a cluster link removes the established trust and deletes the associated identity on the local cluster. If you only run the command on one cluster, the other cluster still has the cluster link identity and trust established (still allowing requests from the linked cluster).
```
