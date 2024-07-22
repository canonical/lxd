(projects-create)=
# How to create and configure projects

You can configure projects at creation time or later.
However, note that it is not possible to modify the features that are enabled for a project when the project contains instances.

## Create a project

`````{tabs}
````{group-tab} CLI
To create a project, use the [`lxc project create`](lxc_project_create.md) command.

You can specify configuration options by using the `--config` flag.
See {ref}`ref-projects` for the available configuration options.

For example, to create a project called `my-project` that isolates instances, but allows access to the default project's images and profiles, enter the following command:

    lxc project create my-project --config features.images=false --config features.profiles=false

To create a project called `my-restricted-project` that blocks access to security-sensitive features (for example, container nesting) but allows snapshots, enter the following command:

    lxc project create my-restricted-project --config restricted=true --config restricted.snapshots=allow
````
````{group-tab} API
To create a project, send a POST request to the `/1.0/projects` endpoint.

You can specify configuration options under the `"config"` field.
See {ref}`ref-projects` for the available configuration options.

For example, to create a project called `my-project` that isolates instances, but allows access to the default project's images and profiles, send the following request:

    lxc query --request POST /1.0/projects --data '{
      "config": {
        "features.images": "false",
        "features.profiles": "false"
      },
      "name": "my-project"
    }'

To create a project called `my-restricted-project` that blocks access to security-sensitive features (for example, container nesting) but allows snapshots, send the following request:

    lxc query --request POST /1.0/projects --data '{
      "config": {
        "restricted": "true",
        "restricted.snapshots": "allow"
      },
      "name": "my-restricted-project"
    }'

See [`POST /1.0/projects`](swagger:/projects/projects_post) for more information.
````
````{group-tab} UI
To create a project, expand the {guilabel}`Project` drop-down and select {guilabel}`+ Create project` at the bottom.

Enter a name and optionally a description for the new project.
You can create the project using the default set of features or select {guilabel}`Customised` to add or remove specific features.
See {ref}`project-features` for more information about the available features.

For example, to create a project called `my-project` that isolates instances, but allows access to the default project's images and profiles:

```{figure} /images/UI/create_project.png
:width: 80%
:alt: Create a project
```

To configure resource limits for the project, select {guilabel}`Resource limits`.

To restrict a project from accessing security-sensitive features, check {guilabel}`Allow custom restrictions on a project level`.
You can then configure the restrictions under {guilabel}`Restrictions`.
See {ref}`project-restrictions` for more information.

For example, to create a project called `my-restricted-project` that blocks access to security-sensitive features (for example, container nesting) but allows snapshots:

1. Check {guilabel}`Allow custom restrictions on a project level`:

   ```{figure} /images/UI/create_restr_project1.png
   :width: 80%
   :alt: Create a restricted project
   ```
1. Configure the restrictions:

   ```{figure} /images/UI/create_restr_project2.png
   :width: 80%
   :alt: Allow snapshots in a restricted project
   ```

````
`````

```{tip}
When you create a project with the default options, {config:option}`project-features:features.profiles` is set to `true`, which means that profiles are isolated in the project.

Consequently, the new project does not have access to the `default` profile of the `default` project and therefore misses required configuration for creating instances (like the root disk).
To fix this, add a root disk device to the project's `default` profile (see {ref}`profiles-set-options` for instructions).
```

(projects-configure)=
## Configure a project

To configure a project, you can either set a specific configuration option or edit the full project.

Some configuration options can only be set for projects that do not contain any instances.

### Set specific configuration options

`````{tabs}
````{group-tab} CLI
To set a specific configuration option, use the [`lxc project set`](lxc_project_set.md) command.

For example, to limit the number of containers that can be created in `my-project` to five, enter the following command:

    lxc project set my-project limits.containers=5

To unset a specific configuration option, use the [`lxc project unset`](lxc_project_unset.md) command.

```{note}
If you unset a configuration option, it is set to its default value.
This default value might differ from the initial value that is set when the project is created.
```
````
````{group-tab} API
To set a specific configuration option, send a PATCH request to the project.

For example, to limit the number of containers that can be created in `my-project` to five, send the following request:

    lxc query --request PATCH /1.0/projects/my-project --data '{
      "config": {
        "limits.containers": "5"
      }
    }'

See [`PATCH /1.0/projects/{name}`](swagger:/projects/project_patch) for more information.
````
````{group-tab} UI
To update the project configuration, select the respective project from the {guilabel}`Project` drop-down.
Then go to {guilabel}`Configuration` and click {guilabel}`Edit configuration` to set or unset any configuration options.
````
`````

### Edit the project

````{tabs}
```{group-tab} CLI
To edit the full project configuration, use the [`lxc project edit`](lxc_project_edit.md) command.
For example:

    lxc project edit my-project
```
```{group-tab} API
To update the entire project configuration, send a PUT request to the project.
For example:

    lxc query --request PUT /1.0/projects/my-project --data '{
      "config": { ... },
      "description": "<description>"
    }'

See [`PUT /1.0/projects/{name}`](swagger:/projects/project_put) for more information.
```
```{group-tab} UI
To UI does not currently support editing the full YAML configuration for a project.
However, you can update several or all configuration options at the same time through the UI.
```
````
