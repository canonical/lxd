(projects-create)=
# How to create and configure projects

You can configure projects at creation time or later.
However, note that it is not possible to modify the features that are enabled for a project when the project contains instances.

## Create a project

To create a project, use the `lxc project create` command.

You can specify configuration options by using the `--config` flag.
See {ref}`ref-projects` for the available configuration options.

For example, to create a project called `my-project` that isolates instances, but allows access to the default project's images and profiles, enter the following command:

    lxc project create my-project --config features.images=false --config features.profiles=false

To create a project called `my-restricted-project` that blocks access to security-sensitive features (for example, container nesting) but allows backups, enter the following command:

    lxc project create my-restricted-project --config restricted=true --config restricted.backups=allow

(projects-configure)=
## Configure a project

To configure a project, you can either set a specific configuration option or edit the full project.

Some configuration options can only be set for projects that do not contain any instances.

### Set specific configuration options

To set a specific configuration option, use the `lxc project set` command.

For example, to limit the number of containers that can be created in `my-project` to five, enter the following command:

    lxc project set my-project limits.containers=5

To unset a specific configuration option, use the `lxc project unset` command.

```{note}
If you unset a configuration option, it is set to its default value.
This default value might differ from the initial value that is set when the project is created.
```

### Edit the project

To edit the full project configuration, use the `lxc project edit` command.
For example:

    lxc project edit my-project
