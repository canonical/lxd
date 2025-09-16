(oidc-entra-id)=
# How to configure authentication with Entra ID

[Entra ID](https://www.microsoft.com/en-gb/security/business/identity-access/microsoft-entra-id) is an Identity and Access Management offering from Microsoft.
It is commonly used as a central location for managing users, groups, roles, and their privileges across many applications and deployments.

LXD supports authentication via [OpenID Connect (OIDC)](https://openid.net/) (see {ref}`authentication-openid`).
Entra ID is an OIDC provider; however, some aspects of the Entra ID OIDC service are non-standard.
In particular, the `access_token` that is returned when a user successfully authenticates using the [device authorization grant](https://datatracker.ietf.org/doc/html/rfc8628) flow is an [opaque string](https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-device-code#successful-authentication-response), and not a [JSON Web Token (JWT)](https://datatracker.ietf.org/doc/html/rfc7519).

The LXD CLI uses the device authorization grant flow to obtain an access token.
When a command is issued, the CLI adds this token to all requests to the LXD API.
For Entra ID, since the token is opaque, LXD is unable to verify it and the command will fail.
Therefore, authentication with Entra ID is only directly supported for the user interface (LXD UI) and not the CLI.

We are working toward full Entra ID support for LXD.
In the meantime, it is possible to use Entra ID if OIDC is only required for the LXD UI.
Alternatively, it is possible to use Entra ID for both the CLI and the user interface by deploying an identity broker such as [Keycloak](https://www.keycloak.org/).

This how-to guide covers configuring {ref}`Entra ID for authentication in the LXD UI only <oidc-entra-id-direct>`, and cover configuring {ref}`Keycloak to act as a broker for Entra ID <oidc-entra-id-keycloak-broker>`.
In both cases, it is assumed that LXD has been initialized and is available remotely via the HTTPS API on port 8443 (see {ref}`server-expose` for instructions).
It is also assumed that you have access to an Entra ID tenant.

(oidc-entra-id-direct)=
## Using Entra ID directly (LXD UI only)

1. In your Entra ID tenant, go to `Identity > Applications > App registrations` in the left panel.

    ```{figure} /images/auth/entra-id/1-app-registrations.png
    Entra ID App registrations
    ```

1. Click `+ New registration`. Then choose a name for the application (for example `LXD`).

    ```{figure} /images/auth/entra-id/2-app-name.png
    Entra ID set application name
    ```

1. Under `Redirect URI (optional)`, select `Public client/native (mobile & desktop)` and type:

       https://<your-LXD-hostname>/oidc/callback

    ```{figure} /images/auth/entra-id/3-redirect-uri.png
    Entra ID set redirection URI
    ```

1. Click `Register`.

1. In the configuration page for your new application, go to `Authentication` in the `Manage` menu.

    ```{figure} /images/auth/entra-id/4-authentication.png
    Entra ID authentication
    ```

1. Scroll down to `Advanced settings`. Under `Allow public client flows`, toggle `Yes` and click `Save`.

    ```{figure} /images/auth/entra-id/5-public-client-flows.png
    Entra ID enable public client flows
    ```

1. In the configuration page for your new application, go to `API permissions` in the `Manage` menu.

    ```{figure} /images/auth/entra-id/6-api-permissions.png
    Entra ID API permissions
    ```

1. Go to `Configured permissions` and click `+ Add a permission`.

    ```{figure} /images/auth/entra-id/7-add-a-permission.png
    Entra ID add a permission
    ```

1. Click `Microsoft Graph` in the right panel.

    ```{figure} /images/auth/entra-id/8-graph-api.png
    Entra ID Graph API permissions
    ```

1. Click `Delegated permissions`.

    ```{figure} /images/auth/entra-id/9-delegated-permissions.png
    Entra ID Graph API delegated permissions
    ```

1. Select all `OpenId permissions`, then click `Add permissions`.

    ```{figure} /images/auth/entra-id/10-openid-permissions.png
    Entra ID OpenID permissions
    ```

1. Above the `Manage` menu, go to `Overview` and copy the `Application (client) ID`.

    ```{figure} /images/auth/entra-id/11-client-id.png
    Entra ID copy client ID
    ```

1. Set this as the client ID in LXD:

       lxc config set oidc.client.id <your-client-id>

1. While still in `Overview`, click `Endpoints` and copy the URL under `OpenID Connect metadata document`.

    ```{figure} /images/auth/entra-id/12-discovery-url.png
    Entra ID tenant discovery URL
    ```

1. Navigate to the URL that you copied. This URL will display some output in JSON format.

1. Copy the URL from the `issuer` field. Then set this as the `oidc.issuer` in LXD:

       lxc config set oidc.issuer <your-issuer>

   Alternatively, execute this command:

       lxc config set oidc.issuer "$(curl <URL that you copied> | jq -r .issuer)"

You can now navigate to the LXD UI in your browser.
When you click `Login with SSO`, you will be redirected to Entra ID to authenticate.

(oidc-entra-id-keycloak-broker)=
## Using Keycloak as an Identity Broker for Entra ID

If you plan to use Keycloak as an identity provider for your production systems, you should follow their guide on [configuring Keycloak for production](https://www.keycloak.org/server/configuration-production).
For this guide, it is assumed that Keycloak is available over HTTPS and that you have created a Keycloak realm with default settings.

1. In your Keycloak realm, go to `Identity providers`.

    ```{figure} /images/auth/entra-id/13-keycloak-identity-providers.png
    Keycloak realm Identity providers
    ```

1. Click `Microsoft`.

    ```{figure} /images/auth/entra-id/14-keycloak-microsoft.png
    Keycloak Microsoft provider
    ```

1. On this page, copy the `Redirect URI`.
   Keep the tab open so that you can return to this page to continue setting up Keycloak.

    ```{figure} /images/auth/entra-id/15-keycloak-broker-redirect-uri.png
    Keycloak broker redirect URI
    ```

1. In your Entra ID tenant, go to `Identity > Applications > App registrations` in the left panel.

    ```{figure} /images/auth/entra-id/1-app-registrations.png
    Entra ID App registrations
    ```

1. Click `+ New registration`. Then choose a name for the application (for example `Keycloak`).

    ```{figure} /images/auth/entra-id/16-app-name-keycloak.png
    Entra ID App name
    ```

1. Under `Redirect URI (optional)`, select `Web` and paste the URL that you copied from Keycloak.
   Then click `Register`.

    ```{figure} /images/auth/entra-id/17-redirect-uri-keycloak.png
    Entra ID set redirection URI
    ```

1. Go to `Certificates & secrets` under `Manage` in your Entra ID tenant.

    ```{figure} /images/auth/entra-id/18-certificates-and-secrets.png
    Entra ID certificates and secrets
    ```

1. Click `+ New client secret`.

    ```{figure} /images/auth/entra-id/19-new-client-secret.png
    Entra ID client secret
    ```

1. In the right panel, click `Add`.
   A new client secret will be displayed.
   Copy the value.

    ```{figure} /images/auth/entra-id/20-copy-client-secret.png
    Entra ID copy client secret
    ```

    ```{note}
    After navigating away from this page, you will no longer be able to view or copy the secret.
    If you forget to copy it, you can delete it and create another one.
    ```

1. In the Keycloak identity provider configuration tab, paste the secret into the `Client secret` field.

    ```{figure} /images/auth/entra-id/21-paste-client-secret.png
    Keycloak paste client secret
    ```

1. In Entra ID, go to the app `Overview` and copy the `Application (client) ID`.

    ```{figure} /images/auth/entra-id/11-client-id.png
    Entra ID copy client ID
    ```

1. Paste the value into the `Client ID` field in the Keycloak tab.

    ```{figure} /images/auth/entra-id/22-paste-client-id.png
    Keycloak paste client ID
    ```

1. In Entra ID, go to the app `Overview` and copy the `Directory (tenant) ID`.

    ```{figure} /images/auth/entra-id/23-copy-tenant-id.png
    Entra ID copy tenant ID
    ```

1. Paste the value into the `Tenant ID` field in the Keycloak tab.

    ```{figure} /images/auth/entra-id/24-paste-tenant-id.png
    Keycloak paste tenant ID
    ```

1. Click `Add`.

1. Follow steps 7 to 11 in the {ref}`above guide <oidc-entra-id-direct>`.
   This allows Keycloak to request the required OpenID scopes.

   We have now configured Keycloak to act as a broker for Entra ID.
   The remaining steps configure Keycloak as the OIDC provider for LXD.

1. In your Keycloak realm, go to `Clients`.

    ```{figure} /images/auth/entra-id/25-keycloak-clients.png
    Keycloak clients
    ```

1. Click `Create client`.

    ```{figure} /images/auth/entra-id/26-keycloak-new-client.png
    Keycloak create client
    ```

1. Set a `Client ID` and a name for the client, then click `Next`.
   The client ID in this example is a random value.
   You can type any value, but it must be unique within the Keycloak realm.

    ```{figure} /images/auth/entra-id/27-keycloak-client-name-and-id.png
    Keycloak client name and ID
    ```

1. In `Authentication flow`, check the `OAuth 2.0 Device Authorization Grant` setting, then click `Next`.

    ```{figure} /images/auth/entra-id/28-keycloak-device-flow.png
    Keycloak device flow
    ```

1. In `Valid redirect URIs`, type `https://<your-LXD-hostname>/oidc/callback`, then click `Save`.

    ```{figure} /images/auth/entra-id/29-keycloak-redirect-uri.png
    Keycloak redirect URI
    ```

1. Go to `Realm settings` under `Configure` in the left panel.

    ```{figure} /images/auth/entra-id/30-keycloak-realm-settings.png
    Keycloak realm settings
    ```

1. Next to `Endpoints`, click `OpenID Endpoint Configuration`.
   This will display some output in JSON format.
   Copy the URL from the `issuer` field, and set this in LXD:

       lxc config set oidc.issuer <your-issuer>

   Alternatively, execute this command:

       lxc config set oidc.issuer "$(curl <configuration-url> | jq -r .issuer)"

1. Configure LXD with the client ID that you configured in Keycloak in step 19.

       lxc config set oidc.client.id <client-id>

You can now log in to LXD via the user interface or via the CLI.
LXD will redirect you to Keycloak to authenticate.
A Microsoft logo will be displayed that will, when clicked, allow you to log in to Keycloak (and therefore LXD) with Entra ID.

### Additional Keycloak settings

It is important to remember that Keycloak is an identity provider in its own right.
Once a user has signed in to Keycloak, information about that user is stored and a session is created.
By default, even with a brokered identity provider, a user may edit their profile details on first log in.
This includes editing their email address.

The information that Keycloak stores about the user is configurable in realm settings.
When using Keycloak as a broker, you should consider preventing users from editing their information in Keycloak.
It might be necessary to configure [mappers](https://www.keycloak.org/docs/latest/server_admin/index.html#_mappers) for the identity provider.
Identity provider mappers configure Keycloak to automatically populate user profile information with fields from the brokered provider.

For more information on identity brokering with Keycloak, please see [their documentation](https://www.keycloak.org/docs/latest/server_admin/index.html#_identity_broker).
