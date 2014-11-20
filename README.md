# lxd [![Build Status](https://travis-ci.org/lxc/lxd.svg?branch=master)](https://travis-ci.org/lxc/lxd)

REST API, command line tool and OpenStack integration plugin for LXC.

LXD is pronounced lex-dee.

## Installing the dependencies

We have exeperienced some problems using gccgo, so for now we recommend using
the golang compiler. We also require that a 1.0+ version of lxc and lxc-dev be
installed. Additionally, some of LXD's dependencies are grabbed from `go get`
via mercurial, so you'll need to have `hg` in your path as well. You can get
these on Ubuntu via:

    sudo apt-get install lxc lxc-dev golang mercurial git pkg-config


## Building the tools

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

And you should have two binaries, one at `/lxc/lxc`, and one at `/lxd/lxd`.

## Running

Right now lxd uses a hardcoded path for all its containers. This will change in
the future, but for now you need to let the user running lxd own /var/lib/lxd:

    sudo mkdir -p /var/lib/lxd
    sudo chown $USER:$USER /var/lib/lxd

You'll also need sub{u,g}ids for the user that lxd is going to run as:

    echo "$USER:1000000:65536" | sudo tee -a /etc/subuid /etc/subgid

Now you can run the daemon:

    ./lxd/lxd &

And connect to it via lxc:

    ./lxc/lxc create foo
    ./lxc/lxc start foo

## Bug reports

Bug reports can be filed at https://github.com/lxc/lxd/issues/new

## Contributing

Fixes and new features are greatly appreciated but please read our
[contributing guidelines](CONTRIBUTING.md) first.

Contributions to this project should be sent as pull requests on github.

## Hacking

Sometimes it is useful to view the raw response that LXD sends; you can do
this by:

    wget --no-check-certificate https://127.0.0.1:443/1.0/ping --certificate=/home/tycho/.config/lxd/cert.pem --private-key=/home/tycho/.config/lxd/key.pem -O - -q

## Support and discussions

We use the LXC mailing-lists for developer and user discussions, you can
find and subscribe to those at: https://lists.linuxcontainers.org

If you prefer live discussions, some of us also hang out in
[#lxcontainers](http://webchat.freenode.net/?channels=#lxcontainers) on irc.freenode.net.
