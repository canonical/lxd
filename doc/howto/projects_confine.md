(projects-confine)=
# How to confine users to specific projects

You restrict users or clients to specific projects.
Projects can be configured with features, limits, and restrictions to prevent misuse.
See {ref}`exp-projects` for more information.

How to confine users to specific projects depends on whether LXD is accessible via the {ref}`HTTPS API <projects-confine-https>`, or via the {ref}`Unix socket <projects-confine-users>`.

(projects-confine-https)=
## Confine users to specific projects on the HTTPS API
```{youtube} https://www.youtube.com/watch?v=4iNpiL-lrXU&t=525s
```

You can confine access to specific projects by restricting the TLS client certificate that is used to connect to the LXD server.
See {ref}`authentication-tls-certs` for detailed information.

```{note}
The UI does not currently support configuring project confinement.
Use the CLI or API to set up confinement.
```

To confine the access from the time the client certificate is added, you must either use token authentication or add the client certificate to the server directly.
If you use password authentication, you can restrict the client certificate only after it has been added.

Follow these instructions:

`````{tabs}
````{group-tab} CLI

If you're using token authentication:

    lxc config trust add --projects <project_name> --restricted

To add the client certificate directly:

    lxc config trust add <certificate_file> --projects <project_name> --restricted

The client can then add the server as a remote in the usual way ([`lxc remote add <server_name> <token>`](lxc_remote_add.md) or [`lxc remote add <server_name> <server_address>`](lxc_remote_add.md)) and can only access the project or projects that have been specified.

To confine access for an existing certificate (either because the access restrictions change or because the certificate was added with a trust password), use the following command:

    lxc config trust edit <fingerprint>

Make sure that `restricted` is set to `true` and specify the projects that the certificate should give access to under `projects`.

```{note}
You can specify the `--project` flag when adding a remote.
This configuration pre-selects the specified project.
However, it does not confine the client to this project.
```

````
````{group-tab} API
If you're using token authentication, create the token first:

    lxc query --request POST /1.0/certificates --data '{
      "name": "<client_name>",
      "projects": ["<project_name>"]
      "restricted": true,
      "token": true,
      "type": "client",
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
      "type": "client",
    }'

The client can then authenticate using this trust token or client certificate and can only access the project or projects that have been specified.

% Include content from [/howto/server_expose.md](/howto/server_expose.md)
```{include} /howto/server_expose.md
   :start-after: <!-- include start authenticate API -->
   :end-before: <!-- include end authenticate API -->
```
````
`````

To confine access for an existing certificate:

````{tabs}
```{group-tab} CLI
Use the following command:

    lxc config trust edit <fingerprint>
```
```{group-tab} API
Send the following request:

    lxc query --request PATCH /1.0/certificates/<fingerprint> --data '{
      "projects": ["<project_name>"],
      "restricted": true
      }'

```
````

Make sure that `restricted` is set to `true` and specify the projects that the certificate should give access to under `projects`.

(projects-confine-users)=
## Confine users to specific LXD projects via Unix socket

```{youtube} https://www.youtube.com/watch?v=6O0q3rSWr8A
```

If you use the [LXD snap](https://snapcraft.io/lxd), you can configure the multi-user LXD daemon contained in the snap to dynamically create projects for all users in a specific user group.

To do so, set the `daemon.user.group` configuration option to the corresponding user group:

    sudo snap set lxd daemon.user.group=<user_group>

Make sure that all user accounts that you want to be able to use LXD are a member of this group.

Once a member of the group issues a LXD command, LXD creates a confined project for this user and switches to this project.
If LXD has not been {ref}`initialized <initialize>` at this point, it is automatically initialized (with the default settings).

If you want to customize the project settings, for example, to impose limits or restrictions, you can do so after the project has been created.
To modify the project configuration, you must have full access to LXD, which means you must be part of the `lxd` group and not only the group that you configured as the LXD user group.
