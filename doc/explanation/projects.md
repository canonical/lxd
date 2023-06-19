(exp-projects)=
# About projects

```{youtube} https://www.youtube.com/watch?v=cUHkgg6TovM
```

You can use projects to keep your LXD server clean by grouping related instances together.
In addition to isolated instances, each project can also have specific images, profiles, networks, and storage.

For example, projects can be useful in the following scenarios:

- You run a huge number of instances for different purposes, for example, for different customer projects.
  You want to keep these instances separate to make it easier to locate and maintain them, and you might want to reuse the same instance names in each customer project for consistency reasons.
  Each instance in a customer project should use the same base configuration (for example, networks and storage), but the configuration might differ between customer projects.

  In this case, you can create a LXD project for each customer project (thus each group of instances) and use different profiles, networks, and storage for each LXD project.
- Your LXD server is shared between multiple users.
  Each user runs their own instances, and might want to configure their own profiles.
  You want to keep the user instances confined, so that each user can interact only with their own instances and cannot see the instances created by other users.
  In addition, you want to be able to limit resources for each user and make sure that the instances of different users cannot interfere with one another.

  In this case, you can set up a multi-user environment with confined projects.

LXD comes with a `default` project.
See {ref}`projects-create` for instructions on how to add projects.

(projects-isolation)=
## Isolation of projects

Projects always encapsulate the instances they contain, which means that instances cannot be shared between projects and instance names can be duplicated in several projects.
When you are in a specific project, you can see only the instances that belong to this project.

Other entities (images, profiles, networks, and storage) can be either isolated in the project or inherited from the `default` project.
To configure which entities are isolated, you enable or disable the respective *feature* in the project.
If a feature is enabled, the corresponding entity is isolated in the project; if the feature is disabled, it is inherited from the `default` project.

For example, if you enable `features.networks` for a project, the project uses a separate set of networks and not the networks defined in the `default` project. If you disable `features.images`, the project has access to the images defined in the `default` project, and any images you add while you're using the project are also added to the `default` project.

See the list of available {ref}`project-features` for information about which features are enabled or disabled when you create a project.

```{note}
You must select the features that you want to enable before starting to use a new project.
When a project contains instances, the features are locked.
To edit them, you must remove all instances first.

New features that are added in an upgrade are disabled for existing projects.
```

(projects-confined)=
## Confined projects in a multi-user environment

If your LXD server is used by multiple users (for example, in a lab environment), you can use projects to confine the activities of each user.
This method isolates the instances and other entities (depending on the feature configuration), as described in {ref}`projects-isolation`.
It also confines users to their own user space and prevents them from gaining access to other users' instances or data.
Any changes that affect the LXD server and its configuration, for example, adding or removing storage, are not permitted.

In addition, this method allows users to work with LXD without being a member of the `lxd` group (see {ref}`security-daemon-access`).
Members of the `lxd` group have full access to LXD, including permission to attach file system paths and tweak the security features of an instance, which makes it possible to gain root access to the host system.
Using confined projects limits what users can do in LXD, but it also prevents users from gaining root access.

### Authentication methods for projects

There are different ways of authentication that you can use to confine projects to specific users:

Client certificates
: You can restrict the {ref}`authentication-tls-certs` to allow access to specific projects only.
  The projects must exist before you can restrict access to them.
  A client that connects using a restricted certificate can see only the project or projects that the client has been granted access to.

RBAC authentication
: If you use {ref}`authentication-rbac`, you can assign roles to users or groups both on a global and on a project basis.
  The roles define what a user is allowed to do within the project.
  This way, you can configure which users are allowed to see, use, or manage each project.

Multi-user LXD daemon
: The LXD snap contains a multi-user LXD daemon that allows dynamic project creation on a per-user basis.
  You can configure a specific user group other than the `lxd` group to give restricted LXD access to every user in the group.

  When a user that is a member of this group starts using LXD, LXD automatically creates a confined project for this user.

  If you're not using the snap, you can still use this feature if your distribution supports it.

See {ref}`projects-confine` for instructions on how to enable and configure the different authentication methods.
