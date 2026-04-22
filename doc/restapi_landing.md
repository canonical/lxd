---
relatedlinks: "[Directly&#32;interacting&#32;with&#32;the&#32;LXD&#32;API](https://ubuntu.com/blog/directly-interacting-with-the-lxd-api)"
myst:
  html_meta:
    description: An index of reference guides for the REST APIs exposed by LXD, the main LXD API and the DevLXD API.
---

(restapi)=
# REST API

These reference guides cover the REST APIs exposed by LXD, its main API and the DevLXD API.

## The main LXD API

The main LXD API can be used for managing instances, networks, storage, and other resources, and for subscribing to the event log.

```{toctree}
:maxdepth: 1

Main API overview <rest-api>
api
Main API extensions <api-extensions>
Events stream <events>
```

## DevLXD API

The DevLXD API allows instances to communicate with their host over a Unix socket.

```{toctree}
:maxdepth: 1

DevLXD API for instances <dev-lxd>
```

## Related topics

{{server_how}}

{{server_exp}}
