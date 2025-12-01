(oidc-keycloak)=
# How to configure Keycloak as login method for LXD

Keycloak is a self-hosted open source tool for authentication. Keycloak supports OIDC and can be used to authenticate users for LXD UI and CLI. This guide shows you how to set up Keycloak as the login method for LXD.

## Using Keycloak to access LXD

1. Set up Keycloak. For this guide, it is assumed that Keycloak is available over HTTPS.
   - If you already have Keycloak installed, follow their guide on [configuring Keycloak for production](https://www.keycloak.org/server/configuration-production).
   - Alternatively, run the development version:
      - Download [Keycloak-25.0.4](https://github.com/keycloak/keycloak/releases/download/25.0.4/keycloak-25.0.4.zip).
      - Extract the files and run `bin/kc.sh start-dev`.
      - Open [`http://localhost:8080`](http://localhost:8080) in your browser and create an admin user with a password.

1. Open the Keycloak Admin Console. For the development version, you can access this at [`http://localhost:8080/admin`](http://localhost:8080/admin). Sign in with the admin user that you created.

1. From the {guilabel}`Keycloak` dropdown in the top left corner of the Admin Console, select {guilabel}`Create realm`. Enter a {guilabel}`Realm name`, such as `lxd-ui-realm`, then click {guilabel}`Create`.

1. From the main navigation, select {guilabel}`Clients`, then click {guilabel}`Create client`. Enter a {guilabel}`Client ID`, such as `lxd-ui-client`, then click {guilabel}`Next`.

1. Under {guilabel}`Capability config`, enable the {guilabel}`OAuth 2.0 Device Authorization Grant` authentication flow to allow both LXD UI and CLI logins. 

   Optionally, to enforce additional authentication via a secret, turn on {guilabel}`Client authentication`. The secret will be available from the client's {guilabel}`Credentials` tab after you finish creating the client, and instructions for sharing it with your LXD server are provided later in this guide. Note: Turning this option on permits UI login only.

   Click {guilabel}`Next`.

1. In the field for {guilabel}`Valid redirect URIs`, enter your LXD UI address, followed by `/oidc/callback`.
   - Example: `https://example.com:8443/oidc/callback`
   - An IP address can be used instead of a domain name.
   - Note `:8443` is the default listening port for the LXD server. It might differ for your setup. You can verify the LXD configuration value `core.https_address` to find the correct port for your LXD server.

   Click {guilabel}`Save`.

1. From the main navigation, select {guilabel}`Users`, then click {guilabel}`Create new user`. Enter a {guilabel}`Username`, then click {guilabel}`Create`.

1. Select the {guilabel}`Credentials` tab for the new user and click {guilabel}`Set password`. Save the new password.

1. Configure the issuer on your LXD server via the CLI. For `<keycloak-realm>`, use the name that you created in step 2. For the `<keycloak-frontend-url>`, use the URL for your Keycloak server, such as `http://192.0.2.1:8080`. If you are running the development version of Keycloak, use `http://localhost:8080`.

       lxc config set oidc.issuer=<keycloak-frontend-url>/realms/<keycloak-realm>

1. Configure the client in LXD with the command below. Use the client id from step 4.

       lxc config set oidc.client.id=<keycloak-client-id>.

1. If you have {guilabel}`Client authentication` on, you need to share the generated secret with your server.

       lxc config set oidc.client.secret=<keycloak-client-secret>.

Now you can access the LXD UI with any browser and use {abbr}`SSO (single sign-on)` login. To use OIDC on the LXD CLI, run `lxc remote add <remote-name> <LXD address> --auth-type oidc` and point a browser to the displayed URL (with user_code) to authenticate.

Users authenticated through Keycloak have no default permissions in the LXD UI. Set up {ref}`LXD authorization groups <manage-permissions>` to grant access to projects and instances and map a LXD authorization group to the user. Note that the user object in LXD is only created on the first login of that user to LXD.
