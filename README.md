# LXD

REST API, command line tool and OpenStack integration plugin for LXC.

LXD is pronounced lex-dee.

To easily see what LXD is about, you can [try it online](https://linuxcontainers.org/lxd/try-it).

## CI status

 * Travis: [![Build Status](https://travis-ci.org/lxc/lxd.svg?branch=master)](https://travis-ci.org/lxc/lxd)
 * Jenkins: [![Build Status](https://jenkins.linuxcontainers.org/job/lxd-github-commit/badge/icon)](https://jenkins.linuxcontainers.org/job/lxd-github-commit/)

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


#### How do I configure alternative storage backends for LXD?

LXD supports various storage backends; below are instructions on how to
configure some of them. By default, we use a simple directory backed storage
mechanism, but we recommend using ZFS for best results.

###### ZFS

First, you need to install the ZFS tooling. On Wily and above this is just:

    sudo apt-get install zfsutils-linux

ZFS has many different ways to procure a zpool, which is what you need to feed
LXD. For example, if you have an extra block device laying around, you can
just:

    sudo zpool create lxd /dev/sdc6

However, if you want to test things out on a laptop or don't have an extra disk
laying around, ZFS has its own loopback driver and can be used directly on a
(sparse) file. To do this, first create the sparse file:

    sudo truncate -s 100G /var/lib/lxd.img

then,

    sudo zpool create lxd /var/lib/lxd.img

Finally, whichever method you used to create your zpool, you need to tell LXD
to use it:

    lxc config set storage.zfs_pool_name lxd

###### BTRFS

The setup for btrfs is fairly simple, just mount /var/lib/lxd (or whatever your
chosen `LXD_DIR` is) as a btrfs filesystem before you start LXD, and you're
good to go. First install the btrfs userspace tools,

    sudo apt-get install btrfs-tools

Now, you need to create a btrfs filesystem. If you don't have an extra disk
laying around, you'll have to create your own loopback device manually:

    sudo truncate -s 100G /var/lib/lxd.img
    sudo losetup /dev/loop0 /var/lib/lxd.img

Once you've got a loopback device (or an actual device), you can create the
btrfs filesystem and mount it:

    sudo mkfs.btrfs /dev/loop0 # or your real device
    sudo mount /dev/loop0 /var/lib/lxd

###### LVM

To set up LVM, the instructions are similar to the above. First, install the
userspace tools:

    sudo apt-get install lvm2 thin-provisioning-tools

Then, if you have a block device laying around:

    sudo pvcreate /dev/sdc6
    sudo vgcreate lxd /dev/sdc6
    lxc config set storage.lvm_vg_name lxd

Alternatively, if you want to try it via a loopback device, there is a script
provided in
[/scripts/lxd-setup-lvm-storage](https://raw.githubusercontent.com/lxc/lxd/master/scripts/lxd-setup-lvm-storage)
which will do it for you. It can be run via:

    sudo apt-get install lvm2
    ./scripts/lxd-setup-lvm-storage -s 10G

And it has a --destroy argument to clean up the bits as well:

    ./scripts/lxd-setup-lvm-storage --destroy



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

    lxc launch ubuntu priv -c security.privileged=true
    lxc config device add priv homedir disk source=/home/$USER path=/home/ubuntu
