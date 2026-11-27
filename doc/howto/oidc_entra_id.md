---
myst:
  html_meta:
    description: Configure LXD to authenticate using Entra ID via OpenID Connect (OIDC) in your tenant.
---

(oidc-entra-id)=
# How to configure authentication with Entra ID

[Entra ID](https://www.microsoft.com/en-gb/security/business/identity-access/microsoft-entra-id) is an Identity and Access Management offering from Microsoft.
It is commonly used as a central location for managing users, groups, roles, and their privileges across many applications and deployments.

LXD supports authentication with Entra ID via [OpenID Connect (OIDC)](https://openid.net/) (see {ref}`authentication-openid`).
To set up access, you need to register two Entra ID apps: one for the LXD UI and one for the LXD CLI.

The steps below assume that LXD is initialized and accessible over HTTPS on port 8443 (see {ref}`server-expose` for instructions).
It is also assumed that you have access to an Entra ID tenant.

(oidc-entra-id-ui-setup)=
## Set up access to the LXD UI

### Register a new app

1. In your Entra ID tenant, go to {guilabel}`Identity` > {guilabel}`Applications` > {guilabel}`App registrations` in the left panel.

    ```{figure} /images/auth/entra-id/1-app-registrations.png
    Entra ID App registrations
    ```

1. Click {guilabel}`+ New registration`. Then choose a name for the application (for example `LXD UI`).
1. Under {guilabel}`Redirect URI (optional)`, select {guilabel}`Web`, and enter:

       https://<your-LXD-hostname>/oidc/callback

1. Click {guilabel}`Register`.

### Add claims to the ID token

1. In the configuration page for your new application, go to {guilabel}`Token configuration` in the {guilabel}`Manage` menu.
1. Click {guilabel}`+ Add optional claim` and select the `ID` token type.
1. Select the `email` and `xms_edov` claims.
1. Click {guilabel}`Add`.

### Add OpenID permissions

1. Go to {guilabel}`API permissions` in the {guilabel}`Manage` menu.
1. Go to {guilabel}`Configured permissions` and click {guilabel}`+ Add a permission`.

    ```{figure} /images/auth/entra-id/7-add-a-permission.png
    Entra ID add a permission
    ```

1. Select {guilabel}`Microsoft Graph` in the right panel.
1. Select {guilabel}`Delegated permissions`.
1. Select all {guilabel}`OpenID permissions`.
1. Click {guilabel}`Add permissions`.

### Add a client secret

```{note}
Configuration of a client secret for the LXD UI app is optional, but strongly recommended.
```

1. In the configuration page for your new application, under {guilabel}`Manage`, select {guilabel}`Certificates & secrets`, then select the {guilabel}`Client secrets` tab.
1. Click {guilabel}`+ New client secret`.
1. Add a description and expiration.
1. Click {guilabel}`Add`.
1. Copy the value of the client secret. You will need this in the next step. Once you leave the page, you cannot view the secret again.

### Configure LXD

1. In LXD, set {config:option}`server-oidc:oidc.client.secret` to the value of the client secret that you copied above:

   ```bash
   lxc config set oidc.client.secret <client-secret>
   ```

1. In the Entra ID tenant, go to {guilabel}`Overview` (above the {guilabel}`Manage` menu) and copy the {guilabel}`Application (client) ID`.

    ```{figure} /images/auth/entra-id/11-client-id.png
    Entra ID copy client ID
    ```

1. Use this ID to set {config:option}`server-oidc:oidc.client.id` in LXD:

   ```bash
   lxc config set oidc.client.id <your-client-id>
   ```

1. While still in {guilabel}`Overview`, click {guilabel}`Endpoints` and copy the URL under {guilabel}`OpenID Connect metadata document`.

    ```{figure} /images/auth/entra-id/12-discovery-url.png
    Entra ID tenant discovery URL
    ```

1. Navigate to the URL that you copied. This URL will display some output in JSON format.
1. Copy the URL from the `issuer` field. Then set this as the {config:option}`server-oidc:oidc.issuer` in LXD:

   ```bash
   lxc config set oidc.issuer <your-issuer>
   ```

   Alternatively, execute this command:

   ```bash
   lxc config set oidc.issuer "$(curl <URL that you copied> | jq -r .issuer)"
   ```

(oidc-entra-id-cli-setup)=
## Set up access to the LXD CLI

### Register a new app

1. In your Entra ID tenant, select {guilabel}`Identity` > {guilabel}`Applications` > {guilabel}`App registrations` in the left panel.
1. Click {guilabel}`+ New registration`. Then enter a name for the application (for example `LXD CLI`).
1. Click {guilabel}`Register`.

### Allow public flows

1. In the configuration page for your new application, go to {guilabel}`Authentication` in the {guilabel}`Manage` menu.

    ```{figure} /images/auth/entra-id/4-authentication.png
    Entra ID authentication
    ```

1. Scroll down to {guilabel}`Advanced settings`. Under {guilabel}`Allow public client flows`, toggle {guilabel}`Yes` and click {guilabel}`Save`.

### Add claims to the access token

1. In the configuration page for the application, go to {guilabel}`Token configuration` in the {guilabel}`Manage` menu.
1. Click {guilabel}`+ Add optional claim` and select the `Access` token type.
1. Select the `email` and `xms_edov` claims.
1. Click {guilabel}`Add`.

### Specify access token version

1. In the configuration page for the application, go to {guilabel}`Manifest` in the {guilabel}`Manage` menu.
1. Set `api.requestedAccessTokenVersion` to `2`.
1. Click {guilabel}`Save`.

### Add a scope for access to the LXD API

1. In the configuration page for the application, go to {guilabel}`Expose an API` in the {guilabel}`Manage` menu.
1. Click {guilabel}`Add` next to the {guilabel}`Application ID URI`.
1. Use the default App ID URI: `api://<lxd-cli-client-id>`.
1. Click {guilabel}`Save`.
1. Click {guilabel}`Add a scope`.
1. Give the scope a name, such as `lxd.access`.
1. Select {guilabel}`Admins and users` for {guilabel}`Who can consent`.
1. Provide a display name and description.
1. Click {guilabel}`Add scope`.
1. Copy the full scope name, which will combine the App ID URI and the scope name: `api://<lxd-cli-client-id>/<scope-name>`. You will need it in the next step.

### Configure LXD

1. In LXD, set {config:option}`server-oidc:oidc.scopes` to the default scopes (`openid`, `email`, `offline_access`, and `profile`), as well as the full Entra ID scope that you copied above:

   ```bash
   lxc config set oidc.scopes "openid email offline_access profile <full-entra-id-scope>"
   ```

1. Above the {guilabel}`Manage` menu, go to {guilabel}`Overview` and copy the {guilabel}`Application (client) ID`.
1. Use this ID to set {config:option}`server-oidc:oidc.audience` in LXD:

   ```bash
   lxc config set oidc.audience <your-client-id>
   ```

1. Use the same ID to set {config:option}`server-oidc:oidc.device.client.id`:

   ```bash
   lxc config set oidc.device.client.id <your-client-id>
   ```

(oidc-entra-id-login)=
## Log in via Entra ID

You can now navigate to the LXD UI in your browser.
When you click {guilabel}`Login with SSO`, you will be redirected to Entra ID to authenticate.

In the terminal, add this LXD server as a remote by running:

    lxc remote add <remote-name> <remote-url> --auth-type oidc

This prompts you to accept the public certificate fingerprint of the remote server, which should match the value for `certificate` shown in `lxc info`.
If you accept, the CLI then displays a unique login code and opens your browser.
In the browser, log in to your Entra ID tenant and enter the code.
Once the CLI process has completed, you can connect to the remote server.

