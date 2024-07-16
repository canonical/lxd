---
discourse: ubuntu:41516
---

(authorization)=
# Remote API authorization

When LXD is {ref}`exposed over the network <server-expose>` it is possible to restrict API access via two mechanisms:

- {ref}`restricted-tls-certs`
- {ref}`fine-grained-authorization`

(restricted-tls-certs)=
## Restricted TLS certificates

It is possible to restrict a {ref}`TLS client <authentication-trusted-clients>` to one or multiple projects.
In this case, the client will also be prevented from performing global configuration changes or altering the configuration (limits, restrictions) of the projects it's allowed access to.

To restrict access, use [`lxc config trust edit <fingerprint>`](lxc_config_trust_edit.md).
Set the `restricted` key to `true` and specify a list of projects to restrict the client to.
If the list of projects is empty, the client will not be allowed access to any of them.

(fine-grained-authorization)=
## Fine-grained authorization

It is possible to restrict {ref}`OIDC clients <authentication-openid>` to granular actions on specific LXD resources.
For example, one could restrict a user to be able to view, but not edit, a single instance.

There are four key concepts that LXD uses to manage these fine-grained permissions:

- **Entitlements**: An entitlement encapsulates an action that can be taken against a LXD API resource type.
   Some entitlements might apply to many resource types, whereas other entitlements can only apply to a single resource type.
   For example, the entitlement `can_view` is available for all resource types, but the entitlement `can_exec` is only available for LXD resources of type `instance`.
- **Permissions**: A permission is the application of an entitlement to a particular LXD resource.
   For example, given the entitlement `can_exec` that is only defined for instances, a permission is the combination of `can_exec` and a single instance, as uniquely defined by its API URL (for example, `/1.0/instances/c1?project=foo`).
- **Identities (users)**: An identity is any authenticated party that makes requests to LXD, including TLS clients.
   When an OIDC client adds a LXD server as a remote, the OIDC client is saved in LXD as an identity.
   Permissions cannot be assigned to identities directly.
- **Groups**: A group is a collection of one or more identities.
   Identities can belong to one or more groups.
   Permissions can be assigned to groups.
   TLS clients cannot currently be assigned to groups.

(permissions)=
### Explore permissions

To discover available permissions that can be assigned to a group, or view permissions that are currently assigned, run the following command:

    lxc auth permission list --max-entitlements 0

The entity type column displays the LXD API resource type, this value is required when adding a permission to a group.

The URL column displays the URL of the LXD API resource.

The entitlements column displays all available entitlements for that entity type.
If any groups are already assigned permissions on the API resource at the displayed URL, they are listed alongside the entitlements that they have been granted.

Some useful permissions at a glance:

- The `admin` entitlement on entity type `server` gives full access to LXD.
  This is equivalent to an unrestricted TLS client or Unix socket access.
- The `project_manager` entitlement on entity type `server` grants access to create, edit, and delete projects, and all resources belonging to those projects.
  However, this permission does not allow access to server configuration, storage pool configuration, or certificate/identity management.
- The `operator` entitlement on entity type `project` grants access to create, edit, and delete all resources belonging to the project against which the permission is granted.
  Members of a group with this permission will not be able to edit the project configuration itself.
  This is equivalent to a restricted TLS client with access to the same project.
- The `user` entitlement on entity type `instance` grants access to view an instance, pull/push files, get a console, and begin a terminal session.
  Members of a group with this entitlement cannot edit the instance configuration.

For a full list, see {ref}`permissions-reference`.

```{note}
Due to a limitation in the LXD client, if `can_exec` is granted to a group for a particular instance, members of the group will not be able to start a terminal session unless `can_view_events` is additionally granted for the parent project of the instance.
We are working to resolve this.
```

(identities)=
### Explore identities

To discover available identities that can be assigned to a group, or view identities that are currently assigned, run the following command:

    lxc auth identity list

The authentication method column displays the method by which the client authenticates with LXD.

The type column displays the type of identity.
Identity types are a superset of TLS certificate types and additionally include OIDC clients.

The name column displays the name of the identity.
For TLS clients, this will be the name of the certificate.
For OIDC clients this will be the name of the client as given by the {abbr}`IdP (identity provider)` (requested via the [profile scope](https://openid.net/specs/openid-connect-basic-1_0.html#Scopes)).

The identifier column displays a unique identifier for the identity within that authentication method.
For TLS clients, this will be the certificate fingerprint.
For OIDC clients, this will be the email address of the client.

The groups column displays any groups that are currently assigned to the identity.
Groups cannot currently be assigned to TLS clients.

```{note}
OIDC clients will only be displayed in the list of identities once they have authenticated with LXD.
```

(manage-permissions)=
### Manage permissions

In LXD, identities cannot be granted permissions directly. Instead, identities are added to groups, and groups are granted permissions.
To create a group, run:

    lxc auth group create <group_name>

To add an identity to a group, run:

    lxc auth identity group add <authentication_method>/<identifier> <group_name>

For example, for OIDC clients:

    lxc auth identity group add oidc/<email_address> <group_name>

The identity is now a member of the group. To add permissions to the group, run:

    lxc auth group permission add <group_name> <entity_type> [<entity_name>] <entitlement> [<key>=<value>...]

Here are some examples:

- `lxc auth group permission add administrator server admin` grants members of `administrator` the `admin` entitlement on `server`.
- `lxc auth group permission add junior-dev project sandbox operator` grants members of `junior-dev` the `operator` entitlement on project `sandbox`.
- `lxc auth group permission add my-group instance c1 user project=default` grants members of `my-group` the `user` entitlement on instance `c1` in project `default`.

Some entity types require more than one supplementary argument to uniquely specify the entity.
For example, entities of type `storage_volume` and `storage_bucket` require an additional `pool=<storage_pool_name>` argument.

(identity-provider-groups)=
### Use groups defined by the identity provider

It is common practice to manage users, roles, and groups centrally via an identity provider (IdP).
In LXD, identity provider groups allow groups that are defined by the IdP to be mapped to LXD groups.
When an OIDC client makes a request to LXD, any groups that can be extracted from the client's identity token are mapped to LXD groups, giving the client the same effective permissions.

To configure IdP group mappings in LXD, first configure your IdP to add groups to identity and access tokens as a custom claim.
This configuration depends on your IdP.
In [{spellexception}`Auth0`](https://auth0.com/), for example, you can add the "roles" that a user has as a custom claim via an [action](https://community.auth0.com/t/how-to-add-roles-and-permissions-to-the-id-token-using-actions/84506).
Alternatively, if {abbr}`RBAC (role-based access control)` is enabled for the audience, a "permissions" claim can be added automatically.
In Keycloak, you can define a [mapper](https://keycloak.discourse.group/t/anyway-to-include-user-groups-into-my-jwt-token/8715) to set Keycloak groups in the token.

Then configure LXD to extract this claim.
To do so, set the value of the {config:option}`server-oidc:oidc.groups.claim` configuration key to the value of the field name of the custom claim:

    lxc config set oidc.groups.claim=<claim_name>

LXD will then expect the identity and access tokens to contain a claim with this name.
The value of the claim must be a JSON array containing a string value for each IdP group name.
If the group names are extracted successfully, LXD will be aware of the IdP groups for the duration of the request.

Next, configure a mapping between an IdP group and a LXD group as follows:

    lxc auth identity-provider-group create <idp_group_name>
    lxc auth identity-provider-group group add <idp_group_name> <lxd_group_name>

IdP groups can be mapped to multiple LXD groups, and multiple IdP groups can be mapped to the same LXD group.

```{important}
LXD does not store the identity provider groups that are extracted from identity or access tokens.
This can obfuscate the true permissions of an identity.
For example, if an identity belongs to LXD group "foo", an administrator can view the permissions of group "foo" to determine the level of access of the identity.
However, if identity provider group mappings are configured, direct group membership alone does not determine their level of access.
The command `lxc auth identity info` can be run by any identity to view a full list of their own effective groups and permissions as granted directly or indirectly via IdP groups.
```
