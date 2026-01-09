(projects-confine)=
# How to confine users to specific projects

You restrict users or clients to specific projects.
Projects can be configured with features, limits, and restrictions to prevent misuse.
See {ref}`exp-projects` for more information.

How to confine users to specific projects depends on whether LXD is accessible via the {ref}`HTTPS API <projects-confine-https>`, or via the {ref}`Unix socket <projects-confine-users>`.

(projects-confine-https)=
## Confine users to specific projects on the HTTPS API
You can confine access to specific projects by restricting the TLS client certificate that is used to connect to the LXD server.
See {ref}`restricted-tls-certs` for more information.
Only certificates returned by `lxc config trust list` can be managed in this way.

```{youtube} https://www.youtube.com/watch?v=4iNpiL-lrXU&t=525s
:title: LXD token based remote authentication
```

```{note}
The UI does not currently support configuring project confinement for certificates of this type.
Use the CLI or API to set up confinement.
```

You can also confine access to specific projects via group membership and {ref}`fine-grained-authorization`.
The permissions of OIDC clients and fine-grained TLS identities must be managed with `lxc auth` subcommands and the `/1.0/auth` API.

To create a TLS client and restrict the client to a single project, follow these instructions:

`````{tabs}
````{group-tab} CLI
##### Create a restricted trust store entry with access to a project
If you're using token authentication:

    lxc config trust add --projects <project_name> --restricted

To add the client certificate directly:

    lxc config trust add <certificate_file> --projects <project_name> --restricted

```{important}
The `--projects` flag requires `--restricted` to be set. Projects can only be used to restrict certificate access when the certificate is marked as restricted.
```

The client can then add the server as a remote in the usual way ([`lxc remote add <server_name> <token>`](lxc_remote_add.md) or [`lxc remote add <server_name> <server_address>`](lxc_remote_add.md)) and can only access the project or projects that have been specified.
```{note}
You can specify the `--project` flag when adding a remote.
This configuration pre-selects the specified project.
However, it does not confine the client to this project.
```

##### Create a fine-grained TLS identity with access to a project
First create a group and grant the group the `operator` entitlement on the project.

    lxc auth group create <group_name>
    lxc auth group permission add <group_name> project <project_name> operator

The `operator` entitlement grants members of the group permission to create and edit resources belonging to that project, but does not grant permission to delete the project or edit its configuration.
See {ref}`fine-grained-authorization` for more details.

Next create a TLS identity and add the identity to the group:

    lxc auth identity create tls/<client_name> [<certificate_file>] --group <group_name>

If `<certificate_file>` is provided the identity will be created directly.
Otherwise, a token will be returned that the client can use to add the LXD server as a remote:

    # Client machine
    lxc remote add <remote_name> <token>

The client will be prompted with a list of projects to use as their default project.
Only the configured project will be presented to the client.
````
````{group-tab} API
##### Create a restricted trust store entry with access to a project
If you're using token authentication, create the token first:

    lxc query --request POST /1.0/certificates --data '{
      "name": "<client_name>",
      "projects": ["<project_name>"]
      "restricted": true,
      "token": true,
      "type": "client"
    }'

% Include content from [/howto/server_expose.md](/howto/server_expose.md)
```{include} /howto/server_expose.md
   :start-after: <!-- include start token API -->
   :end-before: <!-- include end token API -->
```

To instead add the client certificate directly, send the following request:

    lxc query --request POST /1.0/certificates --data '{
      "certificate": "<certificate>",
      "name": "<client_name>",
      "projects": ["<project_name>"]
      "restricted": true,
      "token": false,
      "type": "client"
    }'

The client can then authenticate using this trust token or client certificate and can only access the project or projects that have been specified.

% Include content from [/howto/server_expose.md](/howto/server_expose.md)
```{include} /howto/server_expose.md
   :start-after: <!-- include start gen cert -->
   :end-before: <!-- include end gen cert -->
```
% Include content from [/howto/server_expose.md](/howto/server_expose.md)
```{include} /howto/server_expose.md
   :start-after: <!-- include start cert token -->
   :end-before: <!-- include end cert token -->
```

**Create a fine-grained TLS identity with access to a project**

First create a group and grant the group the `operator` entitlement on the project.

    lxc query --request POST /1.0/auth/groups --data '{
      "name": "<group_name>",
    }'

    lxc query --request PUT /1.0/auth/groups/<group_name> --data '{
      "permissions": [
        {
          "entity_type": "project",
          "url": "/1.0/projects/<project_name>",
          "entitlement": "operator"
        }
      ]
    }'

The `operator` entitlement grants members of the group permission to create and edit resources belonging to that project, but does not grant permission to delete the project or edit its configuration.
See {ref}`fine-grained-authorization` for more details.

Next create a TLS identity and add the identity to the group:

    lxc query --request POST /1.0/auth/identities/tls --data '{
      "name": "<client_name>",
      "groups": ["<group_name>"],
      "token": true
    }'

% Include content from [/howto/server_expose.md](/howto/server_expose.md)
```{include} /howto/server_expose.md
   :start-after: <!-- include start tls identity API -->
   :end-before: <!-- include end tls identity API -->
```

To instead add the client certificate directly, send the following request:

    lxc query --request POST /1.0/certificates --data '{
      "certificate": "<base64 encoded x509 certificate>",
      "name": "<client_name>",
      "groups": ["<group_name>"]
    }'

If the certificate was added directly, the client is now authenticated with LXD.
If a token was used, the client must use it to add their certificate.

% Include content from [/howto/server_expose.md](/howto/server_expose.md)
```{include} /howto/server_expose.md
   :start-after: <!-- include start gen cert -->
   :end-before: <!-- include end gen cert -->
```
% Include content from [/howto/server_expose.md](/howto/server_expose.md)
```{include} /howto/server_expose.md
   :start-after: <!-- include start identity token -->
   :end-before: <!-- include end identity token -->
```
````
`````

To confine access for an existing certificate:

````{tabs}
```{group-tab} CLI
**Trust store entry**

Use the following command:

    lxc config trust edit <fingerprint>

Make sure that `restricted` is set to `true` and specify the projects that the certificate should give access to under `projects`.

**Fine-grained TLS or OIDC identity**

Create a group with the `operator` entitlement on the project:

    lxc auth group create <group_name>
    lxc auth group permission add <group_name> project <project_name> operator

Then add the group to the identity. For TLS identities run:

    lxc auth identity group add tls/<client_name> <group_name>

The `<client_name>` must be unique. If it is not, the certificate fingerprint of the client can be used.

For OIDC identities, run:

    lxc auth identity group add oidc/<client_name> <group_name>

The `<client_name>` must be unique. If it is not, the email address of the client can be used.
```
```{group-tab} API
**Trust store entry**

Send the following request:

    lxc query --request PATCH /1.0/certificates/<fingerprint> --data '{
      "projects": ["<project_name>"],
      "restricted": true
    }'

Make sure that `restricted` is set to `true` and specify the projects that the certificate should give access to under `projects`.

**Fine-grained TLS or OIDC identity**

Create a group with the `operator` entitlement on the project:

    lxc query --request POST /1.0/auth/groups --data '{
      "name": "<group_name>",
    }'

    lxc query --request PUT /1.0/auth/groups/<group_name> --data '{
      "permissions": [
        {
          "entity_type": "project",
          "url": "/1.0/projects/<project_name>",
          "entitlement": "operator"
        }
      ]
    }'

Then add the group to the identity. For TLS identities run:

    lxc query --request PATCH /1.0/auth/identities/tls/<client_name> --data '{
      "groups": ["<group_name>"]
    }'

The `<client_name>` must be unique. If it is not, the certificate fingerprint of the client can be used.

For OIDC identities, run:

    lxc query --request PATCH /1.0/auth/identities/oidc/<client_name> --data '{
      "groups": ["<group_name>"]
    }'

The `<client_name>` must be unique. If it is not, the email address of the client can be used.
```
````

(projects-confine-users)=
## Confine users to specific LXD projects via Unix socket

```{youtube} https://www.youtube.com/watch?v=6O0q3rSWr8A
:title: LXD for multi-user systems
```

If you use the [LXD snap](https://snapcraft.io/lxd), you can configure the multi-user LXD daemon contained in the snap to dynamically create projects for all users in a specific user group.

To do so, set the `daemon.user.group` configuration option to the corresponding user group:

    sudo snap set lxd daemon.user.group=<user_group>

Make sure that all user accounts that you want to be able to use LXD are a member of this group.

Once a member of the group issues a LXD command, LXD creates a confined project for this user and switches to this project.
If LXD has not been {ref}`initialized <initialize>` at this point, it is automatically initialized (with the default settings).

If you want to customize the project settings, for example, to impose limits or restrictions, you can do so after the project has been created.
To modify the project configuration, you must have full access to LXD, which means you must be part of the `lxd` group and not only the group that you configured as the LXD user group.
