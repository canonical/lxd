(server-expose)=
# How to expose LXD to the network

By default, LXD can be used only by local users through a Unix socket.

To expose LXD to the network, set the [`core.https_address`](server) server configuration option.
For example, to allow access to the LXD server on port `8443`, enter the following command:

    lxc config set core.https_address :8443

All remote clients can then connect to LXD and access any image that is marked for public use.

## Authenticate with the LXD server

To be able to access the remote API, clients must authenticate with the LXD server.
There are several authentication methods; see {ref}`authentication` for detailed information.

The recommended method is to add the client's TLS certificate to the server's trust store through a trust token.
To authenticate a client using a trust token, complete the following steps:

1. On the server, enter the following command:

       lxc config trust add

   Enter the name of the client that you want to add.
   The command generates and prints a token that can be used to add the client certificate.
1. On the client, add the server with the following command:

       lxc remote add <remote_name> <token>

   % Include content from [../authentication.md](../authentication.md)
```{include} ../authentication.md
    :start-after: <!-- Include start NAT authentication -->
    :end-before: <!-- Include end NAT authentication -->
```

See {ref}`authentication` for detailed information and other authentication methods.
