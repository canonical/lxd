(server-expose)=
# How to expose LXD to the network

By default, LXD can only be used by local users through a Unix socket.

To expose LXD to the network, you'll need to set `core.https_address`.
All remote clients can then connect to LXD and access any image which was marked for public use.

Trusted clients can be manually added to the trust store on the server with `lxc config trust add` or the `core.trust_password` key can be set allowing for clients to self-enroll into the trust store at connection time by providing the configured password.

More details about authentication can be found [here](../explanation/security.md).

## External authentication

LXD when accessed over the network can be configured to use external authentication through [Candid](https://github.com/canonical/candid).

Setting the `candid.*` configuration keys above to the values matching your Candid deployment will allow users to authenticate through their web browsers and then get trusted by LXD.

For those that have a Canonical RBAC server in front of their Candid server, they can instead set the `rbac.*` configuration keys which are a superset of the `candid.*` ones and allow for LXD to integrate with the RBAC service.

When integrated with RBAC, individual users and groups can be granted various level of access on a per-project basis. All of this is driven externally through the RBAC service.

More details about authentication can be found [here](../explanation/security.md).
