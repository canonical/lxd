(server-expose)=
# How to expose LXD to the network

By default, LXD can be used only by local users through a Unix socket and is not accessible over the network.

To expose LXD to the network, you must configure it to listen to addresses other than the local Unix socket.
To do so, set the {config:option}`server-core:core.https_address` server configuration option.

For example, allow access to the LXD server on port `8443`:

````{tabs}
```{group-tab} CLI
    lxc config set core.https_address :8443
```
```{group-tab} API
    lxc query --request PATCH /1.0 --data '{
      "config": {
        "core.https_address": ":8443"
      }
    }'
```
````

To allow access through a specific IP address, use `ip addr` to find an available address and then set it.
For example:

```{terminal}
:input: ip addr

1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
    inet6 ::1/128 scope host
       valid_lft forever preferred_lft forever
2: enp5s0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc mq state UP group default qlen 1000
    link/ether 00:16:3e:e3:f3:3f brd ff:ff:ff:ff:ff:ff
    inet 10.68.216.12/24 metric 100 brd 10.68.216.255 scope global dynamic enp5s0
       valid_lft 3028sec preferred_lft 3028sec
    inet6 fd42:e819:7a51:5a7b:216:3eff:fee3:f33f/64 scope global mngtmpaddr noprefixroute
       valid_lft forever preferred_lft forever
    inet6 fe80::216:3eff:fee3:f33f/64 scope link
       valid_lft forever preferred_lft forever
3: lxdbr0: <NO-CARRIER,BROADCAST,MULTICAST,UP> mtu 1500 qdisc noqueue state DOWN group default qlen 1000
    link/ether 00:16:3e:8d:f3:72 brd ff:ff:ff:ff:ff:ff
    inet 10.64.82.1/24 scope global lxdbr0
       valid_lft forever preferred_lft forever
    inet6 fd42:f4ab:4399:e6eb::1/64 scope global
       valid_lft forever preferred_lft forever
:input: lxc config set core.https_address 10.68.216.12
```

All remote clients can then connect to LXD and access any image that is marked for public use.

(server-authenticate)=
## Authenticate with the LXD server

To be able to access the remote API, clients must authenticate with the LXD server.
There are several authentication methods; see {ref}`authentication` for detailed information.

The recommended method is to add the client's TLS certificate to the server's trust store through a trust token.
To authenticate a client using a trust token, complete the following steps:

1. On the server, generate a trust token.

   ````{tabs}
   ```{group-tab} CLI
   To generate a trust token, enter the following command on the server:

       lxc config trust add

   Enter the name of the client that you want to add.
   The command generates and prints a token that can be used to add the client certificate.
   ```
   ```{group-tab} API
   To generate a trust token, send a POST request to the `/1.0/certificates` endpoint:

       lxc query --request POST /1.0/certificates --data '{
         "name": "<client_name>",
         "token": true,
         "type": "client"
       }'

   See [`POST /1.0/certificates`](swagger:/certificates/certificates_post) for more information.

   The return value of this query contains an operation that has the information that is required to generate the trust token:

       {
        "class": "token",
        ...
        "metadata": {
           "addresses": [
              "<server_address>"
           ],
           "fingerprint": "<fingerprint>",
           ...
           "secret": "<secret>"
        },
       ...
       }

   Use this information to generate the trust token:

       echo -n '{"client_name":"<client_name>","fingerprint":"<fingerprint>",'\
       '"addresses":["<server_address>"],'\
       '"secret":"<secret>","expires_at":"0001-01-01T00:00:00Z"}' | base64 -w0
   ```
   ````

1. Authenticate the client.

   `````{tabs}
   ````{group-tab} CLI
   On the client, add the server with the following command:

       lxc remote add <remote_name> <token>

   % Include content from [../authentication.md](../authentication.md)
   ```{include} ../authentication.md
       :start-after: <!-- Include start NAT authentication -->
       :end-before: <!-- Include end NAT authentication -->
   ```
   ````
   ````{group-tab} API
   On the client, generate a certificate to use for the connection:

       openssl req -x509 -newkey rsa:2048 -keyout "<keyfile_name>" -nodes \
       -out "<crtfile_name>" -subj "/CN=<client_name>"

   Then send a POST request to the `/1.0/certificates?public` endpoint to authenticate:

       curl -k -s --key "<keyfile_name>" --cert "<crtfile_name>" \
       -X POST https://<server_address>/1.0/certificates \
       --data '{ "password": "<trust_token>" }'

   See [`POST /1.0/certificates?public`](swagger:/certificates/certificates_post_untrusted) for more information.
   ````
   `````

See {ref}`authentication` for detailed information and other authentication methods.
