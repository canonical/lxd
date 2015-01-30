# Introduction
LXD's container creation is entirely image based. That means that the
LXC templates will not be used for container creation. Instead
pre-generated compressed images will be downloaded from image servers
and used.

Users can also turn any container into an image and share it with other
lxd hosts, privately or publicly.

# registry.linuxcontainers.org
A special image server runs at registry.linuxcontainers.org.

Rather than actually serve images, this server serves as the root of
trust for external trusted image servers such as those for the various
distributions, appliance providers, ...

The registry is a static web server accessible over HTTPs which serves a
JSON index of all the trusted remotes including their remote URL,
protocol and list of valid and invalid GPG key(s).

To avoid requiring the use of the GPG server network from the client,
the server also provides a copy of all the keys referenced in the index.

The structure is:
 - /1.0/index.json
 - /1.0/index.json.asc
 - /keys/\<GPG long ID\>
 - /keys/\<GPG long ID\>.asc
 - /certs/\<SHA-256 fingerprint\>
 - /certs/\<SHA-256 fingerprint\>.asc

The structure of the json will be as follow:

    {
        "version": 1,               # Bumped if the format changes in a backward compatible way.
                                    # Backward incompatible changes are done by
                                    # bumping the URL instead from /1.0/ to a new version.

        "generated_at": 1415397644, # UNIX EPOCH

        "servers": [

            { # Example of a legacy lxc image server
                "name": "lxc-images",
                "description": "Images generated from the LXC templates.",                                      # Default description
                "description.fr": "Ça c'est juste un exemple.",                                                 # Translated version (not country specific)
                "description.fr_CH": "Ca c'est Juste un exemple.",                                              # Translated version (country specific)
                "url": "https+lxc-images://images.linuxcontainers.org",                                         # The remote server (mandatory, same format as lxc remote add)
                "arguments": {},                                                                                # dict of strings of values which the remote protocol needs
                "trusted_keys": ["/keys/0xBAEFF88C22F6E216"],                                                   # Trusted keys (mandatory for protocols using GPG validation)
                "trusted_certs": ["/certs/9D9A4864A30EF8FEE9DE727FCEB614DE834CF03D652B1B601848D4EDCCE1FB8B"],   # Trusted SSL certs (mandatory for self-signed servers)
                "min_client_ver": 1,                                                                            # Don't use this unless the client is newer than version 1 (defaults to 0, 0 means any)
                "max_client_ver": 2,                                                                            # Don't use this unless the client is older than version 2 (defaults to 0, 0 means any)
            },

            { # Example of a system-image
                "name": "ubuntu",
                "description": "Ubuntu core images.",                                                           # Default description
                "url": "https+system-image://system-image.ubuntu.com",                                          # The remote server (mandatory, same format as lxc remote add)
                "arguments": {                                                                                  # dict of strings of values which the remote protocol needs
                    "base": "ubuntu-core"                                                                       # In this example, means to only show channels starting by ubuntu-core/*
                },
                "trusted_keys": ["/keys/0x0BFB847F3F272F5B"],                                                   # Trusted keys (mandatory for protocols using GPG validation)
            },

        ],

        "aliases": {    # Short aliases.
            "ubuntu": {
                "server": "lxc-images",
                "arguments": {
                    "distribution": "ubuntu",
                    "release": "trusty",
                    "variant": "default"}
            },
            "ubuntu/devel": {
                "server": "lxc-images",
                "arguments": {
                    "distribution": "ubuntu",
                    "release": "vivid",
                    "variant": "default"}
            },
            "ubuntu/lts": {
                "server": "lxc-images",
                "arguments": {
                    "distribution": "ubuntu",
                    "release": "trusty",
                    "variant": "default"}
            },
            "ubuntu/stable": {
                "server": "lxc-images",
                "arguments": {
                    "distribution": "ubuntu",
                    "release": "utopic",
                    "variant": "default"}
            }
        }
    }


# User experience with the command line tool
## Using the registry
The registry comes pre-configured in the client as the "images" remote,
so you can use it right away without having to first add it.

To list all the trusted image sources in the registry, you may run:

    lxc list images:

This will show you the list of all the trusted image servers. To then
list the images available on one of those, do (for example):

    lxc list images:lxc-images


This is the only remote image store requiring this two step listing but
it's required to avoid hitting all image servers every time and possibly
hanging if one of them is down.


In that setup (fresh installation, no configuration), starting a new
container is as simple as:

    lxc start images:lxc-images/fedora/19/amd64 my-new-fedora-container

or

    lxc start images:ubuntu my-new-ubuntu-container

## Adding a new lxc-images remote
To add a new lxc-images remote, do:

    lxc remote add local-server https+lxc-images://image-server.local

This will then ask you to first validate the certificate fingerprint for
the https server (unless it's a valid certificate), then do the same for
the GPG key. If you approve both, the remote will be added.

Once added, the following will work:

    lxc list local-server:
    lxc start local-server:distro/release/architecture my-new-container


## Adding a new system-image remote
To add a new system-image remote, do:

    lxc remote add local-si-erver https+system-image://system-image-server.local

This will also ask you for the certificate and GPG key if needed but
additionally will also ask for a base channel.

Once added, the following will work:

    lxc list local-si-server:
    lxc start local-si-server:my-channel


# Image transfer and caching
The image servers are remotes of lxc, as such, lxd doesn't know about them at all.

Another problem is that lxd servers will occasionaly be firewalled in a
way where they can't access the public image server, however the client
is perfectly able to reach that same server.

We also can't assume that the link between the client and the server is
fast, this may be a VPN link from another country over a 3G link.

As a result we need to attempt two things:
 * Tell lxd about what image we wish to run, which means passing it all
   information we have about the remote and the image selected by the user.
   lxd will then check if it's in its local cache, if it is, it'll just
   start it. If not, it'll attempt to download it.
 * Should that last part fail, the client will act as a relay,
   downloading the image itself, generate a local cache of the image and
   send that cache entry to the remote server which will in turn cache it
   and spawn the container from it.

When the --always-relay flag of "lxc remote add" is passed, then the
client will jump straight into relay mode and not even ask the server to
try to download the image itself. This flag can be set against either an
image server, in which case any image coming from this server will be
relayed or it can be set against a server meaning that this server is
firewalled and all downloads must be relayed.

As a result, you can do something like this:

    lxc remote add my-images https+lxc-images://image-server.dev.local
    lxc remote add server1 https+lxd://server1.prod.local --always-relay
    lxc remote add server2 https+lxd://server2.prod.local --always-relay
    lxc remote add server3 https+lxd://server3.dev.local
    lxc remote add server4 https+lxd://server4.dev.local
    lxc remote add server5 https+lxd://server5.dev.local

    lxc start my-images:demo-image server1:
    lxc start my-images:demo-image server2:
    lxc start my-images:demo-image server3:
    lxc start my-images:demo-image server4:
    lxc start my-images:demo-image server5:

In this case, server1 and server2 are production servers which can't
access the development image server, so the client will act as a relay
for those two, all the others have fast access directly to the image
server and so will pull from there (unless that fails in which case the
client will act as a relay).


All images come with an expiry, the server will be sent the image and
asked to cache it until it expires so that any other execution of the
image is immediate.
