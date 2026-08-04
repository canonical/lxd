---
myst:
  html_meta:
    description: Configure LXD to authenticate using authentik via OpenID Connect (OIDC) for the LXD UI and CLI.
---

(oidc-authentik)=
# How to configure authentik as login method for the LXD UI and CLI

[authentik](https://goauthentik.io/) is a self-hosted open source identity provider. authentik supports OIDC and can be used to authenticate users for the LXD UI and CLI. This guide shows you how to set up authentik as the login method for the LXD UI and CLI.

You need two authentik providers: a confidential provider for the LXD UI, and a public provider using the device code flow for the LXD CLI.

This guide assumes that authentik is available over HTTPS, and that LXD is initialized and accessible over HTTPS (see {ref}`server-expose`).

## Using authentik to access LXD

1. Set up [authentik](https://goauthentik.io/) using their [installation guide](https://docs.goauthentik.io/install-config/), then open the authentik Admin Console and sign in as an administrator.

1. Create the provider for the LXD UI. From the main navigation, select {guilabel}`Applications` > {guilabel}`Providers`, then click {guilabel}`New Provider` and select {guilabel}`OAuth2/OpenID Provider`.
   - Enter a {guilabel}`Name`, such as `LXD`.
   - Select an {guilabel}`Authorization flow`, either implicit or explicit consent.
   - Set the {guilabel}`Client type` to {guilabel}`Confidential`.
   - Set the {guilabel}`Grant types` to {guilabel}`Authorization Code` and {guilabel}`Refresh Token`.
   - Under {guilabel}`Redirect URIs/Origins`, click {guilabel}`Add Entry` and enter your LXD UI address, followed by `/oidc/callback`.
     - Example: `https://example.com:8443/oidc/callback`
     - An IP address can be used instead of a domain name.
     - Note `:8443` is the default listening port for the LXD server. It might differ for your setup. You can verify the LXD configuration value `core.https_address` to find the correct port for your LXD server.
   - Click {guilabel}`Create`, then reopen the provider and copy the {guilabel}`Client ID` and {guilabel}`Client Secret`. You need both in a later step.

1. Create the provider for the LXD CLI. Select {guilabel}`Applications` > {guilabel}`Providers` again, then click {guilabel}`New Provider` and select {guilabel}`OAuth2/OpenID Provider`.
   - Enter a {guilabel}`Name`, such as `LXD-CLI`.
   - Select an {guilabel}`Authorization flow`, either implicit or explicit consent.
   - Set the {guilabel}`Client type` to {guilabel}`Public`. Public clients have no client secret.
   - Set the {guilabel}`Grant types` to {guilabel}`Refresh Token` and {guilabel}`Device Code`.
   - Under {guilabel}`Redirect URIs/Origins`, click {guilabel}`Add Entry` and enter the same address that you used for the `LXD` provider.
   - Click {guilabel}`Create`, then reopen the provider and copy the {guilabel}`Client ID`. You need it in a later step.
   - While editing the provider, under {guilabel}`Machine-to-machine authentication settings`, move `LXD-CLI` into the {guilabel}`Selected Providers` list for {guilabel}`Federated OAuth2/OpenID Providers`.

1. Make sure that a device code flow is configured on the active brand. The LXD CLI needs it to complete the device code login. If your brand already has a device code flow, skip this step.
   - Select {guilabel}`Flows & Stages` > {guilabel}`Flows`, then click {guilabel}`New Flow`.
     - Enter `default-device-code-flow` as the {guilabel}`Name`, {guilabel}`Title` and {guilabel}`Slug`.
     - Set the {guilabel}`Designation` to {guilabel}`Stage Configuration`.
     - Set the {guilabel}`Authentication` to {guilabel}`Require authentication`.
     - Click {guilabel}`Create Flow`.
   - Select {guilabel}`System` > {guilabel}`Brands`, edit the active brand and under {guilabel}`Default Flows` set its {guilabel}`Device code flow` to `default-device-code-flow`.

1. Create an application for each provider. From the main navigation, select {guilabel}`Applications` > {guilabel}`Applications`, then use {guilabel}`New Application with Existing Provider` and choose the existing provider.
   - Create an application named `LXD` and select the `LXD` provider.
   - Create an application named `LXD-CLI` and select the `LXD-CLI` provider.
   - Note the {guilabel}`Slug` of the `LXD` application. It is part of the issuer URL in the next step.

1. Configure your LXD server with the values that you collected above. For `<authentik-url>`, use the URL of your authentik server, and for `<lxd-application-slug>`, use the slug of the `LXD` application.

   ```bash
   lxc config set oidc.issuer=https://<authentik-url>/application/o/<lxd-application-slug>/
   lxc config set oidc.client.id=<LXD provider client ID>
   lxc config set oidc.client.secret=<LXD provider client secret>
   lxc config set oidc.device.client.id=<LXD-CLI provider client ID>
   lxc config set oidc.audience=<LXD provider client ID>
   ```

   {config:option}`server-oidc:oidc.client.id` and {config:option}`server-oidc:oidc.client.secret` are used by the LXD UI, and {config:option}`server-oidc:oidc.device.client.id` is used by the LXD CLI. Setting `oidc.device.client.id` requires `oidc.client.id` to be set. {config:option}`server-oidc:oidc.audience` must be the client ID of the `LXD` provider, so that LXD accepts the tokens that authentik issues for both providers.

Now you can access the LXD UI with any browser and use {abbr}`SSO (single sign-on)` login. Enter the credentials for authentik.

To use OIDC on the LXD CLI, run:

    lxc remote add <remote-name> <LXD address> --auth-type oidc

This displays a login code and opens a browser. Confirm the code and log in with the credentials for authentik.

Users will have no permissions by default. You must set up {ref}`LXD authorization groups <manage-permissions>` to grant access to projects and instances. For connecting the LXD authorization groups to a user you have two options:

1. Map a LXD authorization group to the user directly. Note, that the user object in LXD will only be created on the first login of that user to LXD.

1. Configure groups in authentik and use automatic mapping to LXD authorization groups as described below.

(oidc-authentik-automatic-group-mapping)=
## Set up automatic group mappings

An admin can set up groups in authentik and add users to those groups. When a user in a group logs in via OIDC, their authentik groups can be mapped to LXD authorization groups. This section details the steps for configuring groups in authentik so that LXD can map them to its authorization groups.

1. In the authentik Admin Console, select {guilabel}`Directory` > {guilabel}`Groups`, then click {guilabel}`New Group`. Enter a {guilabel}`Name`, such as `lxd-admins`, and click {guilabel}`Create`.

1. Open the new group and select its {guilabel}`Users` tab. Add the users that should be part of this group.

1. Tell LXD which claim contains the groups. authentik includes a `groups` claim in the default `authentik default OAuth Mapping: OpenID 'profile'` scope, and LXD requests the `profile` scope by default (see {config:option}`server-oidc:oidc.scopes`):

   ```bash
   lxc config set oidc.groups.claim=groups
   ```

1. Map the authentik group from step 1 to a LXD authorization group. The name of the {ref}`identity provider group <identity-provider-groups>` must match the authentik group name exactly:

   ```bash
   lxc auth identity-provider-group create <authentik-group-name>
   lxc auth identity-provider-group group add <authentik-group-name> <LXD-group-name>
   ```

During the OIDC flow, LXD automatically extracts the claim from the user's `id_token` based on the LXD `oidc.groups.claim` configuration value. The extracted claim is an array of the authentik groups for your user. Those groups are then mapped to LXD authorization groups using the identity provider group created in step 4.
