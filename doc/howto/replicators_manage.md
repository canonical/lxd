---
myst:
  html_meta:
    description: View, configure, rename, and delete LXD replicators and manage their configuration.
---

(howto-replicators-manage)=
# How to manage replicators

(howto-replicators-view)=
## View replicators

`````{tabs}
````{group-tab} CLI

To list all replicators in the current project, run:

    lxc replicator list

To view the configuration for a specific replicator, run:

    lxc replicator show <replicator_name>

To view the current state and job information for a specific replicator, run:

    lxc replicator info <replicator_name>

````
````{group-tab} API

To list all replicators in the current project, send the following request:

    lxc query --request GET /1.0/replicators?project=<project_name>

To display detailed information about each replicator, use {ref}`rest-api-recursion`:

    lxc query --request GET /1.0/replicators?project=<project_name>&recursion=1

See [`GET /1.0/replicators`](swagger:/replicators/replicators_get) and [`GET /1.0/replicators?recursion=1`](swagger:/replicators/replicators_get_recursion1) for more information.

To view the configuration of a specific replicator, send the following request:

    lxc query --request GET /1.0/replicators/<name>?project=<project_name>

See [`GET /1.0/replicators/{name}`](swagger:/replicators/replicator_get) for more information.

To view the current state and job information for a specific replicator, send the following request:

    lxc query --request GET /1.0/replicators/<name>/state?project=<project_name>

See [`GET /1.0/replicators/{name}/state`](swagger:/replicators/{name}/state/replicator_state_get) for more information.

````
`````

(howto-replicators-modify)=
## Configure a replicator

See {ref}`ref-replicator-config` for all available configuration options.

You can edit the entire configuration at once:

````{tabs}
```{group-tab} CLI
To edit a replicator in your default text editor, run:

    lxc replicator edit <replicator_name>

```
```{group-tab} API
To edit a replicator, send the following request:

    lxc query --request PUT /1.0/replicators/<name>?project=<project_name> --data "<replicator_configuration>"

See [`PUT /1.0/replicators/{name}`](swagger:/replicators/replicator_put) for more information.
```
````

You can update a single configuration option for a replicator:

````{tabs}
```{group-tab} CLI

    lxc replicator set <replicator_name> <key>=<value>

To unset a configuration key, run:

    lxc replicator unset <replicator_name> <key>

```
```{group-tab} API

    lxc query --request PATCH /1.0/replicators/<name>?project=<project_name> --data '{"config": {"<key>": "<value>"}}'

See [`PATCH /1.0/replicators/{name}`](swagger:/replicators/replicator_patch) for more information.
```
````

(howto-replicators-rename)=
## Rename a replicator

````{tabs}
```{group-tab} CLI

    lxc replicator rename <replicator_name> <new_name>

```
```{group-tab} API

    lxc query --request POST /1.0/replicators/<name>?project=<project_name> --data '{"name": "<new_name>"}'

See [`POST /1.0/replicators/{name}`](swagger:/replicators/replicator_post) for more information.
```
````

(howto-replicators-delete)=
## Delete a replicator

````{tabs}
```{group-tab} CLI

    lxc replicator delete <replicator_name>

```
```{group-tab} API

    lxc query --request DELETE /1.0/replicators/<name>?project=<project_name>

See [`DELETE /1.0/replicators/{name}`](swagger:/replicators/replicator_delete) for more information.
```
````

## Related topics

How-to guides:

* {ref}`howto-replicators-setup`
* {ref}`howto-replicators-dr`

Reference:

* {ref}`ref-replicator-config`
