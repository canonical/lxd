(authorization)=
# Authorization

When interacting with LXD over the Unix socket, clients have full access to LXD API.
However, it is possible to restrict user access to the LXD API when communicating via remote HTTPS (see {ref}`server-expose` for instructions).
There are three supported authorization methods:

- {ref}`authorization-tls`
- {ref}`authorization-rbac`
- {ref}`authorization-openfga`

(authorization-tls)=
## TLS authorization

LXD natively supports restricting {ref}`authentication-trusted-clients` to one or more projects.
When a client certificate is restricted, the client will also be prevented from performing global configuration changes or altering the configuration (limits, restrictions) of the projects it's allowed access to.

To restrict access, use [`lxc config trust edit <fingerprint>`](lxc_config_trust_edit.md).
Set the `restricted` key to `true` and specify a list of projects to restrict the client to.
If the list of projects is empty, the client will not be allowed access to any of them.

This authorization method is always used if a client authenticates with TLS, regardless of whether another authorization method is configured.

(authorization-rbac)=
## Role Based Access Control (RBAC)

```{youtube} https://www.youtube.com/watch?v=VE60AbJHT6E
```

LXD supports integrating with the Canonical RBAC service, which is included in the [Ubuntu Pro](https://ubuntu.com/pro) subscription.
{abbr}`RBAC (Role Based Access Control)` can be used to limit what an API client is allowed to do on LXD.
This authorization method may only be used with {ref}`authentication-candid`.

In such a setup, authentication happens through Candid, while the RBAC service maintains roles to user/group relationships.
Roles can be assigned to individual projects, to all projects or to the entire LXD instance.

The meaning of the roles when applied to a project is as follows:

- auditor: Read-only access to the project
- user: Ability to do normal life cycle actions (start, stop, ...),
  execute commands in the instances, attach to console, manage snapshots, ...
- operator: All of the above + the ability to create, re-configure and
  delete instances and images
- admin: All of the above + the ability to reconfigure the project itself

To enable RBAC for your LXD server, set the [`rbac.*`](server-options-candid-rbac) server configuration options, which are a superset of the `candid.*` ones and allow for LXD to integrate with the RBAC service.

```{important}
In an unrestricted project, only the `auditor` and the `user` roles are suitable for users that you wouldn't trust with root access to the host.

In a {ref}`restricted project <project-restrictions>`, the `operator` role is safe to use as well if configured appropriately.
```

(authorization-openfga)=
## Open Fine-Grained Authorization (OpenFGA)

LXD supports integrating with [{abbr}`OpenFGA (Open Fine-Grained Authorization)`](https://openfga.dev).
This authorization method is highly granular.
For example, it can be used to restrict user access to a single instance.
OpenFGA authorization is compatible with {ref}`authentication-candid` and {ref}`authentication-openid`.

To use OpenFGA for authorization, you must configure and run an OpenFGA server yourself.
To enable this authorization method in LXD, set the [`openfga.*`](server-options-openfga) server configuration options.
LXD will connect to the OpenFGA server, write the {ref}`openfga-model`, and query this server for authorization for all subsequent requests.

(openfga-model)=
### OpenFGA model

With OpenFGA, access to a particular API resource is determined by the users relationship to it.
These relationships are determined by an [OpenFGA authorization model](https://openfga.dev/docs/concepts#what-is-an-authorization-model).
The LXD OpenFGA authorization model describes API resources in terms of their relationship to other resources, and a relationship a user or group may have with that resource.
Some convenient relations have also been built into the model:

- `server -> admin`: Full access to LXD.
- `server -> operator`: Full access to LXD, without edit access on server configuration, certificates, or storage pools.
- `server -> viewer`: Can view all server level configuration but cannot edit. Cannot view projects or their contents.
- `project -> manager`: Full access to a single project, including edit access.
- `project -> operator`: Full access to a single project, without edit access.
- `project -> viewer`: View access for a single project.
- `instance -> manager`: Full access to a single instance, including edit access.
- `instance -> operator`: Full access to a single  instance, without edit access.
- `instance -> user`: View access to a single instance, plus permissions for `exec`, `console`, and `file` APIs.
- `instance -> viewer`: View access to a single instance.

```{important}
Users that you do not trust with root access to the host should not be granted the following relations:

- `server -> admin`
- `server -> operator`
- `server -> can_edit`
- `server -> can_create_storage_pools`
- `server -> can_create_projects`
- `server -> can_create_certificates`
- `certificate -> can_edit`
- `storage_pool -> can_edit`
- `project -> manager`

Remaining relations may be granted, however you must apply appropriate {ref}`project-restrictions`.
```

The full LXD OpenFGA authorization model is shown below.

```{literalinclude} ../lxd/auth/driver_openfga_model.openfga
---
language: none
---
```
