(oidc-ory)=
# How to configure Ory Hydra as login method for the LXD UI

Ory Hydra is an easy solution to authenticate users for the LXD UI. It supports local users and social sign in through Google, Facebook, Microsoft, GitHub, Apple or others. It does not yet work for the LXD command line. This guide shows you how to set up Ory Hydra as the login method for the LXD UI.

## Using Ory Hydra to access LXD UI

1. Open a free account on [Ory.sh/Hydra](https://www.ory.com/hydra).

1. Once logged into the Ory Console, navigate to {guilabel}`OAuth 2` > {guilabel}`OAuth2 Clients` > {guilabel}`Create OAuth2 Client`.

1. Select the type {guilabel}`Mobile / SPA` and click {guilabel}`Create`. Enter the details for the client:
   - **Client Name**: Choose a name, such as `lxd-ory-client`.
   - **Scope**: Enter `email` and click {guilabel}`Add`, then add `profile` as well.
   - **Redirect URIs**: Enter your LXD UI address, followed by `/oidc/callback`, then click {guilabel}`Add`.
      - Example: `https://example.com:8443/oidc/callback`
      - An IP address can be used instead of a domain name.
      - Note: `:8443` is the default listening port for the LXD server. It might differ for your setup. Use `lxc config get core.https_address` to find the correct port for your LXD server.

1. Select {guilabel}`Create Client` on the bottom of the page.

1. On the {guilabel}`OAuth2 Clients` list, find the {guilabel}`ID` for the client you created. Copy the value and set it in your LXD server configuration with the command:

       lxc config set oidc.client.id=<your OAuth2 Client ID>

1. In the Ory Console, navigate to {guilabel}`OAuth 2` > {guilabel}`Overview`. Find the {guilabel}`Issuer URL` and copy the value. Set this value in your LXD server configuration as issuer with the commands:

       lxc config set oidc.issuer=https://<ory-id>.projects.oryapis.com

Now you can access the LXD UI with any browser and use {abbr}`SSO (single sign-on)` login.

No users exist within ORY by default. New users can use the sign-up link during login. Alternatively, configure Google, Facebook, Microsoft, GitHub, Apple, or another social sign-in provider as described in the [ORY documentation](https://www.ory.com/docs/kratos/social-signin/overview).

Users authenticated through ORY have no default permissions in the LXD UI. Set up {ref}`LXD authorization groups <manage-permissions>` to grant access to projects and instances and map a LXD authorization group to the user. Note that the user object in LXD is only created on the first login of that user to LXD.
