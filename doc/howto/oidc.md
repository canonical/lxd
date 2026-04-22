---
myst:
  html_meta:
    description: An index of how-to guides for LXD single sign-on (SSO) with OpenID Connect (OIDC), including Auth0, Ory Hydra, Keycloak, Entra ID, and Pocket ID.
---

(howto-oidc)=
# Configure single sign-on with OIDC

LXD uses [OpenID Connect (OIDC)](https://openid.net/developers/how-connect-works/) to authenticate users to the web UI and the CLI without storing local passwords. Instead, users are redirected to an external identity provider's login page. For details about this process, refer to {ref}`authentication-openid`.

The following how-to guides provide detailed instructions for the SSO-based identity providers supported by LXD:

```{toctree}
:titlesonly:
:maxdepth: 1

Configure Auth0 </howto/oidc_auth0>
Configure Ory Hydra </howto/oidc_ory>
Configure Keycloak </howto/oidc_keycloak>
Configure Entra ID </howto/oidc_entra_id>
Configure Pocket ID </howto/oidc_pocket_id>
```

## Related topics

How-to guides:

- {ref}`remotes`
- {ref}`server-expose`

Explanation:

- {ref}`authentication`
