(oidc-keycloak)=
# How to configure Keycloak as login method for LXD

Keycloak is a self-hosted open source tool for authentication. Keycloak supports OIDC and can be used to authenticate users for LXD UI and CLI. This guide shows you how to set up Keycloak as the login method for LXD.

## Using Keycloak to access LXD

1. Setup Keycloak, for this guide, it is assumed that Keycloak is available over HTTPS.
   - Following their guide on [configuring Keycloak for production](https://www.keycloak.org/server/configuration-production).
   - Alternatively run the development version: Download [Keycloak-25.0.4](https://github.com/keycloak/keycloak/releases/download/25.0.4/keycloak-25.0.4.zip), extract the file and run `bin/kc.sh start-dev`. Open `http://localhost:8080/` and create an admin user with password.

1. Sign in to Keycloak with an admin account. Select the {guilabel}`Keycloak` dropdown in the top left corner of the admin console. Click {guilabel}`Create realm`. Enter a {guilabel}`Realm name` such as `lxd-ui-realm` and click {guilabel}`Create`.

1. Navigate to {guilabel}`Clients` > {guilabel}`Create client` and enter a {guilabel}`Client ID`, such as `lxd-ui-client`. Then click {guilabel}`Next`.

1. Enable the {guilabel}`OAuth 2.0 Device Authorization Grant` to allow the LXD CLI login. Click {guilabel}`Next`.

1. In the field for {guilabel}`Valid redirect URIs`, enter your LXD UI address, followed by `/oidc/callback`.
   - Example: `https://example.com:8443/oidc/callback`
   - An IP address can be used instead of a domain name.
   - Note `:8443` is the default listening port for the LXD server. It might differ for your setup. You can verify the LXD configuration value `core.https_address` to find the correct port for your LXD server.

   Click {guilabel}`Save`.

1. Go to {guilabel}`Users` > {guilabel}`Create new user`, enter a {guilabel}`Username` and click {guilabel}`Create`.

1. On the user detail page, select {guilabel}`Credentials` and {guilabel}`Set password`. Save the new password.

1. Back in your LXD server, configure the issuer. Use the `<keycloak-realm>` you created in step 2 and the `<keycloak-frontend-url>`, such as `http://192.0.2.1:8080`:

       lxc config set oidc.issuer=<keycloak-frontend-url>/realms/<keycloak-realm>

1. Configure the client in LXD with the command below. Use the client id from step 4.

       lxc config set oidc.client.id=<keycloak-client-id>.

Now you can access the LXD UI with any browser and use {abbr}`SSO (single sign-on)` login. To use OIDC on the LXD CLI, run `lxc remote add <LXD address> --auth-type oidc` and follow the instructions to authenticate.

Users authenticated through Keycloak have no default permissions in the LXD UI. Set up {ref}`LXD authorization groups <manage-permissions>` to grant access to projects and instances and map a LXD authorization group to the user. Note that the user object in LXD is only created on the first login of that user to LXD.
