# LXD [![Build Status](https://travis-ci.org/lxc/lxd.svg?branch=master)](https://travis-ci.org/lxc/lxd)

REST API, command line tool and OpenStack integration plugin for LXC.

LXD is pronounced lex-dee.

## Getting started with LXD

Since LXD development is happening at such a rapid pace, we only provide daily
builds right now. They're available via:

    sudo add-apt-repository ppa:ubuntu-lxc/lxd-git-master && sudo apt-get update
    sudo apt-get install lxd

After you've got LXD installed, you can take your [first steps](#first-steps).

## Building from source

We recommend having the latest versions of liblxc (>= 1.1 required) and CRIU
(>= 1.7 recommended) available for LXD development. Additionally, LXD requires
Golang 1.3 or later to work. All the right verisons dependencies are available
via the LXD PPA:

    sudo apt-get install software-properties-common
    sudo add-apt-repository ppa:ubuntu-lxc/lxd-git-master
    sudo apt-get update
    sudo apt-get install golang lxc lxc-dev mercurial git pkg-config protobuf-compiler golang-goprotobuf-dev xz-utils tar acl

There are a few storage backends for LXD besides the default "directory"
backend. Installing these tools adds a bit to initramfs and may slow down your
host boot, but are needed if you'd like to use a particular backend:

    sudo apt-get install lvm2 thin-provisioning-tools
    sudo apt-get install btrfs-tools

To run the testsuite, you'll also need:

    sudo apt-get install curl gettext jq sqlite3

### Building the tools

LXD consists of two binaries, a client called `lxc` and a server called `lxd`.
These live in the source tree in the `lxc/` and `lxd/` dirs, respectively. To
get the code, set up your go environment:

    mkdir -p ~/go
    export GOPATH=~/go

And then download it as usual:

    go get github.com/lxc/lxd
    cd $GOPATH/src/github.com/lxc/lxd
    make

...which will give you two binaries in $GOPATH/bin, `lxd` the daemon binary,
and `lxc` a command line client to that daemon.

### Machine Setup

You'll need sub{u,g}ids for root, so that LXD can create the unprivileged
containers:

    echo "root:1000000:65536" | sudo tee -a /etc/subuid /etc/subgid

Now you can run the daemon (the --group sudo bit allows everyone in the sudo
group to talk to LXD; you can create your own group if you want):

    sudo -E $GOPATH/bin/lxd --group sudo

## First steps

LXD has two parts, the daemon (the `lxd` binary), and the client (the `lxc`
binary). Now that the daemon is all configured and running (either via the
packaging or via the from-source instructions above), you can import an image:

    $GOPATH/src/github.com/lxc/lxd/scripts/lxd-images import ubuntu --alias ubuntu

With that image imported into LXD, you can now start containers:

    $GOPATH/bin/lxc launch ubuntu

Alternatively, you can also use a remote LXD host as a source of images.
Those will be automatically cached for you for up at container startup time:

    $GOPATH/bin/lxc remote add images images.linuxcontainers.org
    $GOPATH/bin/lxc launch images:centos/7/amd64 centos

## Bug reports

Bug reports can be filed at https://github.com/lxc/lxd/issues/new

## Contributing

Fixes and new features are greatly appreciated but please read our
[contributing guidelines](CONTRIBUTING.md) first.

Contributions to this project should be sent as pull requests on github.

## Hacking

Sometimes it is useful to view the raw response that LXD sends; you can do
this by:

    lxc config set core.trust_password foo
    lxc remote add local 127.0.0.1:8443
    wget --no-check-certificate https://127.0.0.1:8443/1.0 --certificate=$HOME/.config/lxc/client.crt --private-key=$HOME/.config/lxc/client.key -O - -q

## Support and discussions

We use the LXC mailing-lists for developer and user discussions, you can
find and subscribe to those at: https://lists.linuxcontainers.org

If you prefer live discussions, some of us also hang out in
[#lxcontainers](http://webchat.freenode.net/?channels=#lxcontainers) on irc.freenode.net.

## FAQ

#### When I do a `lxc remote add` over https, it asks for a password?

By default, LXD has no password for security reasons, so you can't do a remote
add this way. In order to set a password, do:

    lxc config set core.trust_password SECRET

on the host LXD is running on. This will set the remote password that you can
then use to do `lxc remote add`.

#### How can I live migrate a container using LXD?

**NOTE**: in order to have a migratable container, you need to disable almost
all of the seciruty that LXD provides. We are working on fixing this, but it
requires several kernel changes that take time. You should not use migratable
containers for untrusted workloads right now.

In order to create a migratable container, LXD provides a built in profile
called "migratable". First, launch your container with the following,

     lxc launch -p default -p migratable ubuntu $somename

Ensure you have criu installed on both hosts (`sudo apt-get install criu` for
Ubuntu), and do:

    lxc move host1:$somename host2:$somename

And with luck you'll have migrated the container :)
