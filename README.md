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

We have experienced some problems using gccgo, so for now we recommend using
the golang compiler. We also require that a 1.1+ version of lxc and lxc-dev be
installed. Additionally, some of LXD's dependencies are grabbed from `go get`
via mercurial, so you'll need to have `hg` in your path as well. You can get
these on Ubuntu via:

    sudo apt-get install lxc lxc-dev mercurial git pkg-config protobuf-compiler golang-goprotobuf-dev

### Installing Go

LXD requires Golang 1.3 or later to work.

If running Ubuntu, the easiest way to get it is to use the LXD PPA:

    sudo apt-get install software-properties-common
    sudo add-apt-repository ppa:ubuntu-lxc/lxd-git-master
    sudo apt-get update
    sudo apt-get install golang

In order to be able to extract images and create containers, a few more
dependencies are xz, tar, and setfacl:

    sudo apt-get install xz-utils tar acl

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
    go get -v -d ./...
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
packaging or via the from-source instructions above), you can import some images:

    $GOPATH/src/github.com/lxc/lxd/scripts/lxd-images import lxc ubuntu trusty amd64 --alias ubuntu --alias ubuntu/trusty --alias ubuntu/trusty/amd64
    $GOPATH/src/github.com/lxc/lxd/scripts/lxd-images import lxc debian wheezy amd64 --alias debian --alias debian/wheezy --alias debian/wheezy/amd64

With those two images imported into LXD, you can now start containers:

    $GOPATH/bin/lxc launch ubuntu
    $GOPATH/bin/lxc launch debian debian01

## Bug reports

Bug reports can be filed at https://github.com/lxc/lxd/issues/new

## Contributing

Fixes and new features are greatly appreciated but please read our
[contributing guidelines](CONTRIBUTING.md) first.

Contributions to this project should be sent as pull requests on github.

## Hacking

Sometimes it is useful to view the raw response that LXD sends; you can do
this by:

    lxc config set password foo
    lxc remote add local 127.0.0.1:8443
    wget --no-check-certificate https://127.0.0.1:8443/1.0 --certificate=$HOME/.config/lxc/client.crt --private-key=$HOME/.config/lxc/client.key -O - -q

## Support and discussions

We use the LXC mailing-lists for developer and user discussions, you can
find and subscribe to those at: https://lists.linuxcontainers.org

If you prefer live discussions, some of us also hang out in
[#lxcontainers](http://webchat.freenode.net/?channels=#lxcontainers) on irc.freenode.net.
