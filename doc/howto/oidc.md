(howto-oidc)=
# How to configure single sign-on with OIDC

[OpenID Connect (OIDC)](https://openid.net/developers/how-connect-works/) is an interoperable authentication protocol based on the OAuth 2.0 framework. It allows applications to verify user identities and obtain basic user profile information from an external, trusted identity provider.

LXD uses OIDC to authenticate users to the web UI and the CLI without needing to store a local password. In both cases, LXD redirects the user to the configured identity provider's login page via the browser. Upon successful authentication, the UI or CLI client receives a secure token from the identity provider that validates the session. For more information, see: {ref}`authentication-openid`.

The following how-to guides provide detailed instructions for the SSO-based identity providers supported by LXD:

```{toctree}
:titlesonly:
:maxdepth: 1

How to configure Auth0 </howto/oidc_auth0>
How to configure Ory Hydra </howto/oidc_ory>
How to configure Keycloak </howto/oidc_keycloak>
How to configure Entra ID </howto/oidc_entra_id>
```

## Related topics

How-to guides:

- {ref}`remotes`
- {ref}`server-expose`

Explanation:

- {ref}`authentication`
