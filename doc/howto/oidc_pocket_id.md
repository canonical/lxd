---
myst:
  html_meta:
    description: Configure LXD to authenticate using Pocket ID via OpenID Connect (OIDC) in your tenant.
---

(oidc-pocket-id)=

# How to configure Pocket ID as login method for LXD

Pocket ID is a modern, self-hosted OIDC provider distributed as a single Go binary. It supports only passkeys (no passwords), allowing you to sign into LXD.

## Using Pocket ID to access LXD

1.  Set up [Pocket ID](https://pocket-id.org/docs) using their [installation guide](https://pocket-id.org/docs/setup/installation). This guide assumes that Pocket ID is available over HTTPS.

1.  Create an admin account at `https://<your-app-url>/setup`.

1.  From the main navigation, go to {guilabel}`Administration` > {guilabel}`OIDC Clients`.

1.  From the {guilabel}`Create OIDC Client` section, click {guilabel}`Add OIDC Client`.
    - Enter a name such as `lxd-client`.
    - In the field for {guilabel}`Callback URLs`, enter your LXD UI address, followed by `/oidc/callback`.
      - Example: `https://example.com:8443/oidc/callback`
      - You can use an IP address instead of a domain name.
      - Note `:8443` is the default listening port for the LXD server. It might differ for your setup. You can verify the LXD configuration value `core.https_address` to find the correct port for your LXD server.
    - Enable the {guilabel}`PKCE` option.
    - Optionally, to require users to authenticate again on each authorization, turn on the {guilabel}`Requires Re-Authentication` option.
    - Click {guilabel}`Save`.

1.  In the {guilabel}`Administration` > {guilabel}`OIDC Clients` page, click {guilabel}`Show more details` to see your client configuration.

    ```{figure} /images/auth/pocket-id/pocket-id-show-more-details.png
    Pocket ID client show more details button
    ```

    ```{figure} /images/auth/pocket-id/pocket-id-client.png
    Pocket ID client details
    ```

    - Copy the {guilabel}`Client ID`, {guilabel}`Issuer URL`, {guilabel}`Client Secret` and set them in LXD server configuration:

      ```bash
      lxc config set oidc.client.id=<Client ID>
      lxc config set oidc.issuer=<Issuer URL>
      lxc config set oidc.client.secret=<Client Secret>
      ```

1.  From the main navigation, go to {guilabel}`Administration` > {guilabel}`Users`.
    - From the {guilabel}`Create User` section, click {guilabel}`Add User`. Enter and save the user information.

1.  From the main navigation, go to {guilabel}`Administration` > {guilabel}`User Groups`.
    - From the {guilabel}`Create User Group` section, click {guilabel}`Add Group`. Enter and save the group information.
    - From the {guilabel}`Users` section, select the user created in step 6 to the group and click {guilabel}`Save`.
    - From the {guilabel}`Allowed OIDC Clients` section, select the client created in step 4 and click {guilabel}`Save`.

Now you can access the LXD UI with any browser and use {abbr}`SSO (single sign-on)` login. To use OIDC on the LXD CLI, run `lxc remote add <remote-name> <LXD address> --auth-type oidc` and point a browser to the displayed URL (with `user_code`) to authenticate.

By default, Pocket ID only has an admin user. Follow the [Pocket ID guide](https://pocket-id.org/docs/setup/user-management) to add users manually or sync with an LDAP source.

Users will have no permissions by default. To grant access to projects and instances, you have two options:

1. Set up {ref}`LXD authorization groups <manage-permissions>` to map a LXD authorization group to the user directly. Note, that the user object in LXD will only be created on the first login of that user to LXD.

1. Configure roles in Pocket ID and use automatic mapping to LXD authorization groups as described below.

(oidc-pocket-id-automatic-group-mapping)=

## Set up automatic group mappings

An admin can set up groups in Pocket ID and allocate roles to those groups. When a user in a group logs in via OIDC, their allocated Pocket ID roles can be mapped to LXD authorization groups through custom claims. This section details the steps for configuring roles in Pocket ID and setting up a custom claim so that LXD can map those roles to their authorization groups.

1.  From the main navigation, go to {guilabel}`Administration` > {guilabel}`User Groups`.
    - From the {guilabel}`Manage User Groups` section, select the group you want to assign roles to.
    - From the {guilabel}`Users` section, add and save users to the group.
    - From the {guilabel}`Custom Claims` section, click {guilabel}`Add custom claim`.
    - Enter and save a custom claim and a Pocket ID role in the {guilabel}`key` and {guilabel}`value` fields, respectively.

    ```{figure} /images/auth/pocket-id/pocket-id-custom-claims.png
    Pocket ID custom claims
    ```

1.  Tell LXD to use the custom claim from the previous step to extract Pocket ID roles:

    ```bash
    lxc config set oidc.groups.claim=lxd-role-claim
    ```

1.  Map the Pocket ID role from step 1 to a LXD authorization group:
    ```bash
    lxc auth identity-provider-group create pocketID-admin
    lxc auth identity-provider-group group add pocketID-admin <LXD-group-name>
    ```

During the OIDC flow, LXD automatically extracts the custom claim from the user's `id_token` based on the LXD `oidc.groups.claim` configuration value. The extracted custom claim is an array of roles for your user from Pocket ID. Those roles are then mapped to LXD authorization groups using the identity provider group created in step 3.
