(server-configure)=
# How to configure the LXD server

See {ref}`server` for all configuration options that are available for the LXD server.

If the LXD server is part of a cluster, some of the options apply to the cluster, while others apply only to the local server, thus the cluster member.
In the {ref}`server` option tables, options that apply to the cluster are marked with a `global` scope, while options that apply to the local server are marked with a `local` scope.

## Configure server options

````{tabs}
```{group-tab} CLI
You can configure a server option with the following command:

    lxc config set <key> <value>

For example, to allow remote access to the LXD server on port 8443, enter the following command:

    lxc config set core.https_address :8443

In a cluster setup, to configure a server option for a cluster member only, add the `--target` flag.
For example, to configure where to store image tarballs on a specific cluster member, enter a command similar to the following:

    lxc config set storage.images_volume my-pool/my-volume --target member02
```
```{group-tab} API
Send a PATCH request to the `/1.0` endpoint to update one or more server options:

    lxc query --request PATCH /1.0 --data '{
      "config": {
        "<key>": "<value>",
        "<key>": "<value>"
      }
    }'

For example, to allow remote access to the LXD server on port 8443, send the following request:

    lxc query --request PATCH /1.0 --data '{
      "config": {
        "core.https_address": ":8443"
      }
    }'

In a cluster setup, to configure a server option for a cluster member only, add the `target` parameter to the query.
For example, to configure where to store image tarballs on a specific cluster member, send a request similar to the following:

    lxc query --request PATCH /1.0?target=member02 --data '{
      "config": {
        "storage.images_volume": "my-pool/my-volume"
      }
    }'

See [`PATCH /1.0`](swagger:/server/server_patch) for more information.
```
````

## Display the server configuration

````{tabs}
```{group-tab} CLI
To display the current server configuration, enter the following command:

    lxc config show

In a cluster setup, to show the local configuration for a specific cluster member, add the `--target` flag.
```
```{group-tab} API
Send a GET request to the `/1.0` endpoint to display the current server environment and configuration:

    lxc query --request GET /1.0

In a cluster setup, to show the local environment and configuration for a specific cluster member, add the `target` parameter to the query:

    lxc query --request GET /1.0?target=<cluster_member>

See [`GET /1.0`](swagger:/server/server_get) for more information.
```
````

## Edit the full server configuration

````{tabs}
```{group-tab} CLI
To edit the full server configuration as a YAML file, enter the following command:

    lxc config edit

In a cluster setup, to edit the local configuration for a specific cluster member, add the `--target` flag.
```
```{group-tab} API
To update the full server configuration, send a PUT request to the `/1.0` endpoint:

    lxc query --request PUT /1.0 --data '<server_configuration>'

In a cluster setup, to update the full server configuration for a specific cluster member, add the `target` parameter to the query:

    lxc query --request PUT /1.0?target=<cluster_member> '<server_configuration>'

See [`PUT /1.0`](swagger:/server/server_put) for more information.
```
````
