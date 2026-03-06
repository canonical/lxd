---
myst:
  html_meta:
    description: Configure LXD to authenticate using Pocket ID via OpenID Connect (OIDC) in your tenant.
---

(oidc-pocket-id)=

# How to configure Pocket ID as login method for LXD

Pocket ID is a simple, passwordless, self-hosted OIDC provider that allows you to sign into LXD with a passkey and no need for a password.

## Using Pocket ID to access LXD

1.  Set up [Pocket ID](https://pocket-id.org/docs) using their [installation guide](https://pocket-id.org/docs/setup/installation). For this guide, it is assumed that Pocket ID is available over HTTPS.
1.  Sign in to the admin account at `https://<your-app-url>/setup`.
1.  From the main navigation, go to `Adminstration` > `OIDC Clients`.
1.  From the `Create OIDC Client` section, click `Add OIDC Client`.
    - Enter a name such as `lxd-client`.
    - In the field for `Callback URLs`, enter your LXD UI address, followed by `/oidc/callback`.
      - Example: `https://example.com:8443/oidc/callback`
      - An IP address can be used instead of a domain name.
      - Note `:8443` is the default listening port for the LXD server. It might differ for your setup. You can verify the LXD configuration value `core.https_address` to find the correct port for your LXD server.
      - In the field for `Logout Callback URLs`, enter your LXD UI address, followed by `/oidc/logout`.
    - Turn on the `Public Client` option.
    - Optionally, to require users to authenticate again on each authorization, turn on the `Requires Re-Authentication` option.
    - Click `Save`.
1.  Click `Show more details` to see your client configuration.
    - Copy the `Client ID` and set it in LXD server configuration using

       lxc config set oidc.client.id=<Client ID>

    - Copy the `Issuer URL` and set it in your LXD server configuration using

       lxc config set oidc.issuer=<Issuer URL>

1.  From the main navigation, go to `Adminstration` > `Users`.
    - From the `Create User` section, click `Add User`. Enter the user information and click `Save`.
1.  From the main navigation, go to `Adminstration` > `User Groups`.
    - From the `Create User Group` section, click `Add Group`. Enter the group information and click `Save`.
    - From the `Users` section, add the user created in step 6 to the group and click `Save`.
    - From the `Allowed OIDC Clients` section, add the client created in step 4.
    - Click `Save`.

Now you can access the LXD UI with any browser and use {abbr}`SSO (single sign-on)` login. To use OIDC on the LXD CLI, run `lxc remote add <remote-name> <LXD address> --auth-type oidc` and point a browser to the displayed URL (with user_code) to authenticate.

No users other than the admin exist within Pocket ID by default. Follow the [Pocket ID guide](https://pocket-id.org/docs/setup/user-management) to add users manually or sync with an LDAP source.

Users authenticated through Pocket ID have no default permissions in the LXD UI. Set up {ref}`LXD authorization groups <manage-permissions>` to grant access to projects and instances and map a LXD authorization group to the user. Note that the user object in LXD is only created on the first login of that user to LXD.
