---
myst:
  html_meta:
    description: An index of how-to guides for common LXD server and client operations, including how to configure the server and set up OIDC single sign-on.
---

(lxd-server)=
# LXD server and client

These how-to guides cover common operations related to the LXD server and client.

## Configure the LXD server

```{toctree}
:titlesonly:
:maxdepth: 1

Configure the LXD server </howto/server_configure>
Expose LXD to the network </howto/server_expose>
Configure single sign-on with OIDC </howto/oidc>
```

## Configure the LXD CLI client

The LXD CLI client (`lxc`) can be configured to use remote servers instead of the local LXD daemon. For convenience, aliases can be set up for frequently used commands.

```{toctree}
:titlesonly:

Add remote servers </remotes>
Add command aliases </howto/lxc_alias>
```

## Related topics

{{server_exp}}

{{server_ref}}
