(oidc-auth0)=
# How to configure Auth0 as login method for the LXD UI and CLI

Auth0 is a flexible, drop-in solution to add authentication and authorization services to your applications. Auth0 supports OIDC and can be used to authenticate users for the LXD UI and CLI. This guide shows you how to set up Auth0.com as the login method for the LXD UI and CLI.

## Using Auth0.com to access LXD

1. Open a free account on [Auth0.com](https://auth0.com/).

1. Once logged into the Auth0 web interface, select {guilabel}`Applications` > {guilabel}`Applications` in the side panel.
   - You need to create two applications. One is for the LXD UI and the other is for the LXD CLI.

1. First, create an application that will be used for authentication in the LXD UI by selecting {guilabel}`+ Create Application`.
   - Give the application a name, e.g. `LXD UI`.
   - Select {guilabel}`Regular Web Application`.
   - Click {guilabel}`Create`.

1. Go to the {guilabel}`Settings` tab of your new application.
   - Scroll to the {guilabel}`Allowed Callback URLs` field in this tab and enter your LXD UI address, followed by `/oidc/callback`.
     - Example: `https://example.com:8443/oidc/callback`
     - An IP address can be used instead of a domain name.
       - Note `:8443` is the default listening port for the LXD server. It might differ for your setup. You can verify the LXD configuration value `core.https_address` to find the correct port for your LXD server.
   - Enable {guilabel}`Allow Refresh Token Rotation`.
   - Scroll down to {guilabel}`Advanced Settings` and select the {guilabel}`Grant Types` tab.
   - Enable {guilabel}`Authorization code` and {guilabel}`Refresh Token`. Other grant types can be disabled.
   - Select {guilabel}`Save`.

1. Navigate to {guilabel}`Basic Information` near the top of the {guilabel}`Settings` tab.
   - Locate the {guilabel}`Domain` field. Copy the value and add the `https://` prefix and the `/` suffix as in the example below.
     This is your OIDC issuer for LXD. Set this value in your LXD server configuration with the command:

         lxc config set oidc.issuer=https://dev-example.us.auth0.com/
   - Locate the {guilabel}`Client ID` and {guilabel}`Client Secret` fields. Copy the values and use them in your LXD server configuration:

         lxc config set oidc.client.id=<Client ID>
         lxc config set oidc.client.secret=<Client Secret>

   - Now you can access the LXD UI with any browser and use {abbr}`SSO (single sign-on)` login. Enter the credentials for Auth0.

1. Now create an application to be used by the LXD CLI. To do this, go back to {guilabel}`Applications` > {guilabel}`Applications` in the side panel and click {guilabel}`+ Create Application`.
   - Give the application a name, e.g. `LXD CLI`.
   - Select the default {guilabel}`Native` application type.
   - Click {guilabel}`Create`.

1. Go to the {guilabel}`Settings` tab of your new application.
   - Enable {guilabel}`Allow Refresh Token Rotation`.
   - Scroll down to {guilabel}`Advanced Settings` and select the {guilabel}`Grant Types` tab.
   - Enable {guilabel}`Device Code` and {guilabel}`Refresh Token`. Other grant types can be disabled.
   - Select {guilabel}`Save`.

1. Navigate to {guilabel}`Basic Information` near the top of the {guilabel}`Settings` tab.
   - Locate the {guilabel}`Client ID` field and use it in your LXD server configuration:

         lxc config set oidc.device.client.id=<Client ID>
   - You can now access LXD using the CLI with

         lxc remote add <remote-name> <LXD-address> --auth-type oidc
     This will open a browser where you must confirm the device code displayed in the terminal window, and log in with the credentials for Auth0.

Users will have no permissions by default. You must set up {ref}`LXD authorization groups <manage-permissions>` to grant access to projects and instances. For connecting the LXD authorization groups to a user you have two options:

1. Map a LXD authorization group to the user directly. Note, that the user object in LXD will only be created on the first login of that user to LXD.

1. Configure roles in Auth0 and use automatic mapping to LXD authorization groups as described below.

(oidc-auth0-automatic-group-mapping)=
## Set up automatic group mappings
An admin can set up multiple users in Auth0 and allocate roles to those users. When a user logs in via OIDC, their allocated Auth0 roles can be mapped to LXD authorization groups through custom claims. This section details the steps for configuring roles in Auth0 and setting up a custom claim so that LXD can map those roles to its authorization groups.

1. In the left panel of the Auth0 interface, select {guilabel}`User Management` > {guilabel}`Roles`, create some roles with suitable names. Note that these roles are global for the Auth0 tenant.

1. Under {guilabel}`User Management` > {guilabel}`Users`, click {guilabel}`Create User`. Provide an email and password and create the user.

1. Select on the {guilabel}`Roles` tab in the user detail page, then click the {guilabel}`Assign Roles` button. Select the roles you created in step 1.

1. You must set up a custom action on Auth0 to set the custom claim on both the `id_token` and `access_token` during the OIDC login flow.
    - In the main navigation, under {guilabel}`Actions` > {guilabel}`Library`, click the {guilabel}`Create Action` button. Select {guilabel}`Create Custom Action`.
       - **Name**: Give the action a suitable name like `roles-in-id-token`.
       - **Trigger**: Login / Post Login
       - **Runtime**: The recommended default
    - Click {guilabel}`Create`. This causes a code editor to open.
    - In the code editor, insert the code snippet shown below:

    ```javascript
    exports.onExecutePostLogin = async (event, api) => {
      if (event.authorization) {
        api.idToken.setCustomClaim(`global-roles`, event.authorization.roles);
        api.accessToken.setCustomClaim(`global-roles`, event.authorization.roles);
      }
    };
    ```

    - Select {guilabel}`Deploy`.
    - Once the action is deployed, go to {guilabel}`Actions` > {guilabel}`Triggers` > {guilabel}`post-login`. Under the {guilabel}`Add Action` > {guilabel}`Custom` tab, drag the action you just created and drop it in between the {guilabel}`Start` and {guilabel}`Complete` nodes of the Login flow. Select {guilabel}`Apply` to save the changes.

1. Navigate to the LXD UI. First authenticate with the UI using a trusted certificate so that you can configure server settings without permission issues.

1. In the LXD UI, under {guilabel}`settings`, find `oidc.groups.claim`. Set it to the custom claim configured in step 4. Using the current example, the custom claim is `global-roles`. Alternatively, use the command line: `lxc config set oidc.groups.claim=global-roles`.

1. Continuing in the LXD UI, navigate to {guilabel}`Permissions` > {guilabel}`IDP groups` and click {guilabel}`Create IDP Group`. Here you can map roles from Auth0 to LXD authorization groups. For each {ref}`identity provider group <identity-provider-groups>` created in LXD, the name of the identity provider group must match a role you have created in Auth0, and it should also map to one or more LXD authorization groups. Alternatively, use the command line:

       lxc auth identity-provider-group create <auth0-role-name>
       lxc auth identity-provider-group group add <auth0-role-name> <LXD-group-name>

1. Lastly, you log in as a user with roles assigned in Auth0. During the OIDC flow, LXD extracts the roles set by Auth0 based on the LXD `oidc.groups.claim` configuration value. The extracted custom claim is an array of roles for your user from Auth0. Those roles are then mapped to LXD authorization groups using the identity provider group created in step 7.
