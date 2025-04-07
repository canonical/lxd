(howto-cluster-links-manage)=
# How to manage cluster links

(howto-cluster-links-list)=
## List cluster links

````{tabs}
```{group-tab} CLI
To list cluster links, enter the following command:

    lxc cluster link list
```
```{group-tab} API
To list all cluster links (that you have permission to see), send the following request:

    lxc query --request GET /1.0/cluster/links

To display information about each project, use {ref}`rest-api-recursion`:

    lxc query --request GET /1.0/cluster/links?recursion=1

See [`GET /1.0/cluster/links`](swagger:/cluster-links/cluster_links_get) and  [`GET /1.0/cluster/links?recursion=1`](swagger:/cluster-links/cluster_links_get_recursion1) for more information.
```
````

To view cluster link configuration for a specific cluster, use the following command:

````{tabs}
```{group-tab} CLI
To view the full configuration of a cluster link, enter the following command:

    lxc cluster link show <name>
```
```{group-tab} API
To view the full configuration of a cluster link, send the following request:

    lxc query --request GET /1.0/cluster/links/<name>

See [`GET /1.0/cluster/links/{name}`](swagger:/cluster-links/{name}/cluster_link_get) for more information.
```
````

To view detailed information about a specific cluster link, including its state:

````{tabs}
```{group-tab} CLI
To view detailed information about a specific cluster link, enter the following command:

    lxc cluster link info <name>
```
```{group-tab} API
To view detailed information and state of a cluster link, send the following request:

    lxc query --request GET /1.0/cluster/links/<name>/state

See [`GET /1.0/cluster/links/{name}/state`](swagger:/cluster-links/{name}/state/cluster_link_state_get) for more information.
```
````

(howto-cluster-links-permissions)=
## Manage cluster link permissions

To modify the permissions of a cluster link, add its identity to authentication groups. See {ref}`manage-permissions` for more information on how to manage permissions.

(howto-cluster-links-modify)=
## Configure a cluster link

See {ref}`cluster-link-config` for more details on cluster link configuration options.

To configure a cluster link, use the `edit` command:

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

To update a property for a cluster link, use the `set` command with the `--property` flag.

````{tabs}
```{group-tab} CLI
To modify a specific property, enter the following command:

    lxc cluster link set <name> --property <key>=<value>

For example, to update the description property:

    lxc cluster link set cluster_b --property description="Backup cluster in data center 2"
```
```{group-tab} API
To modify a specific property, send the following request:

    lxc query --request PATCH /1.0/cluster/links/<name> --data '{"description": "Backup cluster in data center 2"}'

See [`PATCH /1.0/cluster/links/{name}`](swagger:/cluster-links/{name}/cluster_link_patch) for more information.
```
````

To update a configuration option for a cluster link, use the `set` command.

````{tabs}
```{group-tab} CLI
To modify a specific configuration option, enter the following command:

    lxc cluster link set <name> <key>=<value>
```
```{group-tab} API
To modify a specific configuration option, send the following request:

    lxc query --request PATCH /1.0/cluster/links/<name> --data '{"config": <config>}'

See [`PATCH /1.0/cluster/links/{name}`](swagger:/cluster-links/{name}/cluster_link_patch) for more information.
```
````

(howto-cluster-links-delete)=
## Delete a cluster link

To delete a cluster link, use the `delete` command:

````{tabs}
```{group-tab} CLI
To delete a cluster link, enter the following command:

    lxc cluster link delete <name>
```
```{group-tab} API
To delete a cluster link, send the following request:

    lxc query --request DELETE /1.0/cluster/links/<name>

See [`DELETE /1.0/cluster-links/{name}`](swagger:/cluster-links/{name}/cluster_link_delete) for more information.
```
````

```{note}
Deleting a cluster link removes the established trust and deletes the associated identity on the local cluster. To fully disconnect the clusters, run the command on both clusters.
```
