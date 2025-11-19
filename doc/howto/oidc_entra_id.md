---
myst:
  html_meta:
    description: Configure LXD to authenticate using Entra ID via OpenID Connect (OIDC) in your tenant.
---

(oidc-entra-id)=
# How to configure authentication with Entra ID

[Entra ID](https://www.microsoft.com/en-gb/security/business/identity-access/microsoft-entra-id) is an Identity and Access Management offering from Microsoft.
It is commonly used as a central location for managing users, groups, roles, and their privileges across many applications and deployments.

LXD supports authentication Entra ID via [OpenID Connect (OIDC)](https://openid.net/) (see {ref}`authentication-openid`).
To configure authentication with Entra ID, follow the steps below.

We assumed that LXD is initialized and accessible over HTTPS on port 8443 (see {ref}`server-expose` for instructions).
It is also assumed that you have access to an Entra ID tenant.

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

In the terminal, add this LXD server as a remote by running:

    lxc remote add <remote-name> <remote-url> --auth-type oidc

This prompts you to accept the public certificate fingerprint of the remote server, which should match the value for `certificate` shown in `lxc info`.
If you accept, the CLI then displays a unique login code and opens your browser.
In the browser, log in to your Entra ID tenant and enter the code.
Once the CLI process has completed, you can connect to the remote server.
