# LXD

REST API, command line tool and OpenStack integration plugin for LXC.

LXD is pronounced lex-dee.

To easily see what LXD is about, you can [try it online](https://linuxcontainers.org/lxd/try-it).

## Status

* GoDoc: [![GoDoc](https://godoc.org/github.com/lxc/lxd?status.svg)](https://godoc.org/github.com/lxc/lxd)
* Jenkins (Linux): [![Build Status](https://jenkins.linuxcontainers.org/job/lxd-github-commit/badge/icon)](https://jenkins.linuxcontainers.org/job/lxd-github-commit/)
* Travis (macOS): [![Build Status](https://travis-ci.org/lxc/lxd.svg?branch=master)](https://travis-ci.org/lxc/lxd/)
* AppVeyor (Windows): [![Build Status](https://ci.appveyor.com/api/projects/status/rb4141dsi2xm3n0x/branch/master?svg=true)](https://ci.appveyor.com/project/lxc/lxd/)
* Weblate (translations): [![Translation status](https://hosted.weblate.org/widgets/linux-containers/-/svg-badge.svg)](https://hosted.weblate.org/projects/linux-containers/lxd/)


## Getting started with LXD

Since LXD development is happening at such a rapid pace, we only provide daily
builds right now. They're available via:

    sudo add-apt-repository ppa:ubuntu-lxc/lxd-git-master && sudo apt-get update
    sudo apt-get install lxd

Because group membership is only applied at login, you then either need to
close and re-open your user session or use the "newgrp lxd" command in the
shell you're going to interact with lxd from.

    newgrp lxd

After you've got LXD installed and a session with the right permissions, you
can take your [first steps](#first-steps).

#### Getting started with LXD on Windows

LXD server is not available on Windows, but it is possible to use
[`lxc` client](https://ci.appveyor.com/project/lxc/lxd/branch/master/artifacts)
with
[some limitations](https://github.com/lxc/lxd/issues?utf8=%E2%9C%93&q=is%3Aissue%20is%3Aopen%20windows)
to control remote containers.


## Using the REST API
The LXD REST API can be used locally via unauthenticated Unix socket or remotely via SSL encapsulated TCP.

#### via Unix socket
```bash
curl --unix-socket /var/lib/lxd/unix.socket \
    -H "Content-Type: application/json" \
    -X POST \
    -d @hello-ubuntu.json \
    lxd/1.0/containers
```

#### via TCP
TCP requires some additional configuration and is not enabled by default.
```bash
lxc config set core.https_address "[::]:8443"
```
```bash
curl -k -L \
    --cert ~/.config/lxc/client.crt \
    --key ~/.config/lxc/client.key \
    -H "Content-Type: application/json" \
    -X POST \
    -d @hello-ubuntu.json \
    "https://127.0.0.1:8443/1.0/containers"
```
#### JSON payload
The `hello-ubuntu.json` file referenced above could contain something like:
```json
{
    "name":"some-ubuntu",
    "ephemeral":true,
    "config":{
        "limits.cpu":"2"
    },
    "source": {
        "type":"image",
        "mode":"pull",
        "protocol":"simplestreams",
        "server":"https://cloud-images.ubuntu.com/releases",
        "alias":"14.04"
    }
}
```

## Building from source

We recommend having the latest versions of liblxc (>= 2.0.0 required) and CRIU
(>= 1.7 recommended) available for LXD development. Additionally, LXD requires
Golang 1.5 or later to work. All the right versions dependencies are available
via the LXD PPA:

    sudo apt-get install software-properties-common
    sudo add-apt-repository ppa:ubuntu-lxc/lxd-git-master
    sudo apt-get update
    sudo apt-get install acl dnsmasq-base git golang liblxc1 lxc-dev make pkg-config rsync squashfs-tools tar xz-utils

There are a few storage backends for LXD besides the default "directory"
backend. Installing these tools adds a bit to initramfs and may slow down your
host boot, but are needed if you'd like to use a particular backend:

    sudo apt-get install lvm2 thin-provisioning-tools
    sudo apt-get install btrfs-tools

To run the testsuite, you'll also need:

    sudo apt-get install curl gettext jq sqlite3 uuid-runtime pyflakes pep8 shellcheck bzr

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
packaging or via the from-source instructions above), you can create a container:

    $GOPATH/bin/lxc launch ubuntu:14.04

Alternatively, you can also use a remote LXD host as a source of images.
One comes pre-configured in LXD, called "images" (images.linuxcontainers.org)

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

## Upgrading

The `lxd` and `lxc` (`lxd-client`) binaries should be upgraded at the same time with:

    apt-get update
    apt-get install lxd lxd-client

## Support and discussions

We use the LXC mailing-lists for developer and user discussions, you can
find and subscribe to those at: https://lists.linuxcontainers.org

If you prefer live discussions, some of us also hang out in
[#lxcontainers](http://webchat.freenode.net/?channels=#lxcontainers) on irc.freenode.net.


## FAQ

#### How to enable LXD server for remote access?

By default LXD server is not accessible from the networks as it only listens
on a local unix socket. You can make LXD available from the network by specifying
additional addresses to listen to. This is done with the `core.https_address`
config variable.

To see the current server configuration, run:

    lxc config show

To set the address to listen to, find out what addresses are available and use
the `config set` command on the server:

    ip addr
    lxc config set core.https_address 192.168.1.15

#### When I do a `lxc remote add` over https, it asks for a password?

By default, LXD has no password for security reasons, so you can't do a remote
add this way. In order to set a password, do:

    lxc config set core.trust_password SECRET

on the host LXD is running on. This will set the remote password that you can
then use to do `lxc remote add`.

You can also access the server without setting a password by copying the client
certificate from `.config/lxc/client.crt` to the server and adding it with:

    lxc config trust add client.crt


#### How do I configure LXD storage?

LXD supports btrfs, directory, lvm and zfs based storage.

First make sure you have the relevant tools for your filesystem of
choice installed on the machine (btrfs-progs, lvm2 or zfsutils-linux).

By default, LXD comes with no configured network or storage.
You can get a basic configuration done with:

    lxd init

"lxd init" supports both directory based storage and ZFS.
If you want something else, you'll need to use the "lxc storage" command:

    lxc storage create default BACKEND [OPTIONS...]
    lxc profile device add default root disk path=/ pool=default

BACKEND is one of "btrfs", "dir", "lvm" or "zfs".

Unless specified otherwise, LXD will setup loop based storage with a sane default size.

For production environments, you should be using block backed storage
instead both for performance and reliability reasons.

#### How can I live migrate a container using LXD?

Live migration requires a tool installed on both hosts called
[CRIU](http://criu.org), which is available in Ubuntu via:

    sudo apt-get install criu

Then, launch your container with the following,

    lxc launch ubuntu $somename
    sleep 5s # let the container get to an interesting state
    lxc move host1:$somename host2:$somename

And with luck you'll have migrated the container :). Migration is still in
experimental stages and may not work for all workloads. Please report bugs on
lxc-devel, and we can escalate to CRIU lists as necessary.

#### Can I bind mount my home directory in a container?

Yes. The easiest way to do that is using a privileged container:

1.a) create a container.

    lxc launch ubuntu privilegedContainerName -c security.privileged=true

1.b) or, if your container already exists.

        lxc config set privilegedContainerName security.privileged true
2) then.

    lxc config device add privilegedContainerName shareName disk source=/home/$USER path=/home/ubuntu

#### How can I run docker inside a LXD container?

To run docker inside a lxd container, you must be running a kernel with cgroup
namespaces (Ubuntu 4.4 kernel or newer, or upstream 4.6 or newer), and must
apply the docker profile to your container.

    lxc launch ubuntu:xenial my-docker-host -p default -p docker

Note that the docker profile does not provide a network interface, so the
common case will want to compose the default and docker profiles.

Also note that Docker coming from [upstream](https://apt.dockerproject.org/repo) doesn't currently run as is inside the lxd container. Look at issue [#2621](https://github.com/lxc/lxd/issues/2621) for more details. You need to download the docker coming from Ubuntu (docker.io package) to get this working. So once you are in the lxd container run

    sudo apt-get install -y docker.io runc containerd

The container must be using the Ubuntu 1.10.2-0ubuntu4 or newer docker package.
