# Introduction

The "lxc" command is the main tool used by users to interact with lxd when
running it outside of OpenStack. The command is available to all users and can
be used to manage any local or remote resources provided they have the
credentials to do so.

# Available commands
One of the core aspects of lxd is its extensibility.  Plugins can be written
for both the server and client side of the solution in order to add extra
features to the system. Some of those plugins will be strictly server side
(such as integrating live migration with physical hardware) while some others
will have both a server and client plugin.

This means that the list of commands in this document only covers the core
commands and their default arguments.  Extra commands and arguments to existing
commands may then be added by the plugins.

# Remote operations

The lxc command line tool is designed to manage lxd hosts as well as connect
to a variety of remote resources.

The list of remote servers and their credentials is only stored in the client,
the servers don't know about each other nor do they have to. The client is the
one initiating any cross-server communication by instructing the two servers to
start talking to each other. Should that fail, for example because of a
firewall between the two servers, the client will then act as a relay
forwarding the data stream between the two servers.

In the case of image servers, the client will be the one querying the image
server, looking up the image that should be spawned, it will then instruct the
server to download it into its cache or should that fail, the client will
download it and stream it over to the server. Once in the server's cache, it'll
be spawned from there.

* * *

# Resources
The lxc command interacts with resources, those will typically be things like
containers, snapshots, images or container hosts but the list may grow
especially through the use of plugins.

Rather than having a set of command for each type of resources, a standard URI
scheme has been designed to identify any resource, local or remote. All
commands will typically take at least one of those URIs as their argument.

Basic URI scheme:

    [remote:]<resource>/<sub-resource>/<sub-sub-resource>/...

Some examples with the "status" command:

Command                                 | Result
:------                                 | :-----
lxc status                              | Show general status of the local lxcd instance
lxc status dakara:                      | Show general status of the remote host "dakara"
lxc status c1                           | Show status of the local "c1" container container
lxc status images:ubuntu/trusty/amd64   | Show status of a remote Ubuntu 14.04 64bit image
lxc status dakara:c2/yesterday          | Show status of the "yesterday" snapshot of container "c2" on remote host "dakara"


This URI scheme is designed to be very specific (no ambiguity) and as short as
possible.

* * *

# Commands
## Overview

Command     | Description
:------     | :----------
ping        | Ping the lxd instance to see if it is online.
start       | Create and/or start a container (option for ephemeral)
stop        | Stop a container
status      | Show the status of a resource (host, container, snapshot, ...)
list        | Lists available resources (containers, snapshots, remotes, ...)
config      | Change container settings (quotas, notes, OS metadata, ...)
copy        | Make a copy of a container, image or snapshot
move        | Move a container or image either to rename it or to migrate it
delete      | Delete a resource (container, snapshot, image, ...)
snapshot    | Make a snapshot (stateful or not) of a container
restore     | Restore a snapshot of a container
shell       | Spawn a shell within the container (or any other command)
file        | Transfer files in and out of the container
remote      | Add a remote resource (host, image server or other equipment)
publish     | Publish a local snapshot or container as a bundled image

* * *

## ping

**Arguments**

    [resource]

**Description**

Sends a ping to the lxd instance, and wait for the daemon's version number as a
response.

* * *

## start

**Arguments**

    <resource> [new resource] [--ephemeral|-e] [--profile|-p <profile>...]

**Description**

start is used to start a container, either an existing one or
creating a new one based on an existing container, container snapshot or image.

If the resource is read-only (an image or snapshot for example), a copy of it
will be made using a random name (UUID) before starting it. Passing --ephemeral
will make lxd create a temporary container which will be destroyed when
shutdown, if passed with an existing container, a copy of it will be made. --
profile is used to apply a configuration profile (or multiple ones if passed
multiple times) to the newly created container, when passed with an existing
container, it will only append the configuration profile for that run.

**Examples**

Command                                        | Result
:------                                        | :-----
lxc start images:ubuntu/trusty/amd64           | Create a new local container using a UUID as its name based on the Ubuntu 14.04 amd64 image.
lxc start images:ubuntu/precise/i386 dakara:   | Create a new remote container on "dakara" using a UUID as its name based on the Ubuntu 14.04 i386 image.
lxc start dakara:c2/yesterday c3               | Create a new local container called "c3" based on the remote container snapshot "yesterday" of "c2" from "dakara".
lxc start c2                                   | Start local container "c2"
lxc start c2 c3 -e                             | Create a new local container called "c3" based on local container "c2" and have it disappear on exit.
lxc start images:ubuntu c1 -p with-nesting     | Create a new local container called "c1" based on the recommended Ubuntu image for this host (latest LTS, same architecture) and run it with a profile allowing container nesting.

* * *

## stop

**Arguments**

    <resource> [--kill|-k] [--timeout|-t]

**Description**

Stops the container. By default does a clean shutdown by sending
SIGPWR to the container’s init process if after 30s the container is still
running, an error is displayed. The 30s timeout can be overridden using --
timeout. Alternatively, --kill can be passed which will cause the container to
be immediately killed (timeout is meaningless in this case).

**Examples**

Command                     | Result
:------                     | :-----
lxc stop c1                 | Do a clean shutdown of local container "c1"
lxc stop dakara:c1 -t 10    | Do a clean shutdown of remote container "c1" on "dakara" with a reduced timeout of 10s
lxc stop dakara:c1 -k       | Kill the remote container "c1" on "dakara"
 
* * *

## status

**Arguments**

    [resource]

**Description**

Prints information about any resource. If run against a host it
will print its lxd version, kernel version, LXC version, disk usage and any
other information relevant to the host (extendable by plugins). When run
against a container, its status will be shown, if running, IP addresses will be
displayed alongside resource consumption. When run against a snapshot, the
snapshot tree will be shown (if relevant to the backing store) as well as the
name and description of the snapshot and its size. If run against an image, the
description, image metadata, image source and cache status will be displayed.

**Examples**

Command                                         | Result
:------                                         | :-----
lxc status                                      | Displays local host status
lxc status dakara:                              | Displays the host status of remote host "dakara"
lxc status c1                                   | Displays the status of local container "c1"
lxc status dakara:c2                            | Displays the status of remote container "c2" on "dakara"
lxc status dakara:c2/yesterday                  | Displays the status of snapshot "yesterday" for remote container "c2" on "dakara"
lxc status images:ubuntu/trusty/amd64/20140910  | Displays the status of the Ubuntu 14.04 64bit image made on the 10th of September 2014
lxc status images:ubuntu/trusty                 | Displays the status of the default Ubuntu 14.04 image available from the image store "images".

* * *

## list
**Arguments**

    [resource] [filters] [format]

**Description**

Lists all the available resources. By default it will list the
local containers, snapshots and images. Each comes with some minimal status
information (status, addresses, ... configurable if needed by passing a list of
fields to display).

For containers, a reasonable default would be to show the name, state, ipv4
addresses, ipv6 addresses, memory and disk consumption.
Snapshots would be displayed below their parent containers and would re-use the
name, state and disk consumption field, the others wouldn’t be relevant and
will be displayed as "-".

Images aren’t tied to containers so those will be displayed in a separate table
after the containers, relevant fields for images would be their name (e.g
ubuntu/trusty/amd64/20140915), description (e.g. Ubuntu 14.04 LTS 64bit),
source (e.g. images.linuxcontainers.org) and status (available, cached, ...).

**Examples**

Command             | Result
:------             | :-----
lxc list            | Shows the list of local containers, snapshots and images.
lxc list images:    | Shows the list of available images from the "images" remote.
lxc list dakara:    | Shows the list of remote containers, snapshots and images on "dakara".
lxc list c1:        | Shows the entry for the local container "c1" as well as any snapshot it may have.


* * *

## config

**Arguments**

    get <resource> <key>
    set <resource> <key> <value>
    set-profile <resource> <profile name>[,<second profile name>, ...]
    show <resource>
    unset <resource> <key>
    profile create <profile name>
    profile copy <source profile name> <target profile name>
    profile delete <profile name>
    profile list [remote] [filters]
    profile get <profile name> <key>
    profile move <profile name> <new profile name>
    profile set <profile name> <key> <value>
    profile show <profile name>
    profile unset <profile name> <key>

**Description**

Probably one of the most complex commands, it allows querying and
setting all the configuration options available on a given container, snapshot,
image or any other kind of supported resource. It also allows creating
profiles, profiles are a set of configuration keys which aren’t directly tied
to a container or any other resource. Instead it’s the profile itself which is
tied to the resource it’s configuring. Multiple profiles may be applied to a
container, they override each other in the order they are provided and
container-specific settings override any that value coming from a profile.
get/set/show/unset can be run on a remote resource by using the usual syntax.

The same goes for profiles, it’s possible to create, delete, list and setup
profiles on a remote host. The one limitation is that a container can only
reference local profiles, so profiles need to be copied across hosts or be
moved around alongside the containers.

Also note that removing a profile or moving it off the host will fail if any
local container still references it.

**Examples**

Command                                                                         | Result
:------                                                                         | :-----
lxc config c1 set lxc.aa\_profile=unconfined                                    | Set the apparmor profile of local container c1 to "unconfined".
lxc config profile create loop-mount                                            | Create a new "loop-mount" profile.
lxc config profile set loop-mount lxc.cgroup.devices.allow "c 7:\* rwm"         | Allow access to /dev/loop.
lxc config profile set loop-mount lxc.aa\_profile=lxc-default-with-mounting     | Set an appropriate apparmor profile.
lxc config profile copy loop-mount dakara:                                      | Copy the resulting profile over to "dakara".
lxc config profile show loop-mount                                              | Show all the options associated with the loop-mount profile and all the containers using it.
lxc config show c1                                                              | Show the configuration of the c1 container, starting by the list of profiles it’s based on, then the container specific settings and finally the resulting overall configuration.
lxc config set-profile c1 loop-mount,nesting                                    | Set the profiles for container c1 to be loop-mount followed by nesting.
lxc config set-profile c1 ""                                                    | Unset any assigned profile for container "c1".

* * *

## copy

**Arguments**

    <source resource> <destination resource>

**Description**

Creates a copy of a resource, this typically only applies to
containers where it’s used to clone a container into another one, either on the
same host or remotely. It can also be used to turn an image or a snapshot into
a container without starting it (so similar to what start does in that regard
except for the configuration and startup step).

**Examples**

Command                                 | Result
:------                                 | :-----
lxc copy c1 c2                          | Create a container called "c2" which is a copy of container "c1" with its hostname changed and a fresh MAC address.
lxc copy c1 dakara:                     | Copy container "c1" to remote host "dakara" still keeping the name "c1" on the target. A copy c1 new MAC address will be generated for the copy.
lxc copy c1 dakara: c2                  | Same as above but also rename the container and change its hostname.
lxc copy images:ubuntu/trusty/amd64 c1  | Copies a read-only image of Ubuntu 14.04 64bit as container "c1". This is very similar to the equivalent start command except that in this instance the container won’t start.

* * *

## move

**Arguments**

    <source resource> <destination resource> [--stateful]

**Description**

Moves a resource, either locally (rename) or remotely (migration).
This requires the container to be offline, unless --stateful is passed in which
case the container’s state will be dumped prior to the move and then restore on
destination (for local stateful rename or for remote live migration).

**Examples**

Command                         | Result
:------                         | :-----
lxc move c1 c2                  | Rename container c1 into c2, this requires c1 to be offline and will update its hostname.
lxc move c1 dakara:             | Move c1 to "dakara" in a stateless manner. This requires c1 to be offline.
lxc move c1 dakara:c2           | Move c1 to dakara as "c2" in a stateless manner. The hostname will be updated. Requires c1 to be offline.
lxc move c1 c2 --stateful       | Rename container c1 into c2 while it’s running. This will dump its state to disk, kill the container, rename it, update its hostname and restore the container state.
lxc move c1 dakara: --stateful  | Live migrates container c1 to dakara. This will first stream the filesystem content over to dakara, then dump the container state to disk, sync the state and the delta of the filesystem, restore the container on the remote host and then wipe it from the source host.

* * *

## delete

**Arguments**

    <resource>

**Description**
Destroy a resource (e.g. container) and any attached data
(configuration, snapshots, ...). This requires the resource in question be unused
at the time.

**Examples**

Command                         | Result
:------                         | :-----
lxc delete c1                   | Removes the c1 container, its configuration and any snapshot it may have.
lxc delete c1/yesterday         | Removes the "yesterday" snapshot of "c1".
lxc delete dakara:c2/yesterday  | Removes the "yesterday" snapshot for "c2" on remote host "dakara".

* * *

## snapshot

**Arguments**

    <resource> <snapshot name> [--stateful]

**Description**

Makes a read-only snapshot of a resource (typically a container).
For a container this will be a snapshot of the container’s filesystem,
configuration and if --stateful is passed, its current running state.

**Examples**

Command                                         | Result
:------                                         | :-----
lxc snapshot c1 it-works                        | Creates "it-works" snapshot of container c1.
lxc snapshot dakara:c1 pre-upgrade --stateful   | Make a pre-dist-upgrade snapshot of container "c1" running on "dakara". Allows for a very fast recovery time in case of problem.

* * *

## restore

**Arguments**

    <resource> <snapshot name> [--stateful]

**Description**

Set the current state of a resource back to what it was when it
was snapshotted. All snapshots are kept as they are, only the current state is
discarded and replaced by that from the snapshot. This requires the container
be stopped unless --stateful is passed and the snapshot contained a running
container state in which case the container will be killed, reset to the
snapshot and the container’s state restored.

**Examples**

Command                                         | Result
:------                                         | :-----
lxc restore c1 it-works                         | Restores the c1 container back to its "it-works" snapshot state.
lxc restore dakara:c1 pre-upgrade --stateful    | Restores a pre-dist-upgrade snapshot of container "c1" running on "dakara". Allows for a very fast recovery time in case of problem.

* * *

## shell

**Arguments**

    <container> [command...]

**Description**

Without any argument, this command will simply spawn a shell
inside the container. Alternatively if a command line is passed, then this one
will be executed instead of the default shell. This command may grow extra
arguments to allow controlling the default environment, uid/gid, ...

**Examples**

Command                                                 | Result
:------                                                 | :-----
lxc shell c1                                            | Spawns /bin/bash in local container c1
tar xcf - /opt/myapp \| lxc shell dakara:c2 tar xvf -   | Makes a tarball of /opt/myapp with the stream going out to stdout, then have that piped into lxc shell connecting to a receiving tar command in container running on remote host "dakara".

* * *

## file

**Arguments**

    file push [-R] [--uid=UID] [--gid=GID] [--mode=MODE] <source> [<source>...] <target>
    file pull [-R] <source> [<source>...] <target>

**Description**
Copies file to or from the container. Supports rewriting the uid/gid/mode and recursive transfer.

**Examples**

Command                                                 | Result
:------                                                 | :-----
lxc file push -R test c1/root/                          | Recursively copy the directory called "test" into the "c1" container in /root/
lxc file push --uid=0 --gid=0 test.sh dakara:c2/root/   | Push test.sh as /root/test.sh inside container "c2" on host "dakara", rewrite the uid/gid to 0/0.
lxc file pull dakara:c2/etc/hosts /tmp/                 | Grab /etc/hosts from container "c2" on "dakara" and write it as /tmp/hosts on the client.

* * *

## remote

**Arguments**

    add <name> <URI>
    delete <name>
    list
    rename <old name> <new name>
    set-default <name>

**Description**
Manages remote resources. Those will typically be either lxd
servers or some kind of image store.

Initially the following list of remote server types would be supported:

Scheme                  | Description
:-----                  | :----------
unix+lxd://Unix         | socket (or abstract if leading @) access to lxd
https+lxd://            | Communication with lxd over the network (https)
https+system-image://   | Communication with a system-image server
https+lxc-images://     | Communication with a LXC image server

By default lxc would come with the following remotes:

Name        | URI                                               | Description
:---        | :--                                               | :----------
local       | unix+lxd:///var/lib/lxd/sock                      | Communication to the local lxd (hidden if not present)
images      | https+system-image://images.linuxcontainers.org   | Main server for official lxd images, provided with delta support
lxc-images  | https+lxc-images://images.linuxcontainers.org     | The existing LXC image server providing basic system container images for most supported distributions and architectures.

The default remote is "local", this allows simple operations with local
resources without having to specify local: in front of all of their names. This
behavior can be changed by using the set-default argument. On a system without
a local lxd, the first manually added remote should be automatically set as
default.

Protocol auto-detection will happen so that adding a source solely based on its
name will work too, assuming it doesn’t support multiple protocols.

**Examples**

Command                                                         | Result
:------                                                         | :-----
lxc remote add dakara dakara.local                              | Add a new remote called "dakara" using its avahi DNS record and protocol auto-detection.
lxc remote add vorash https+lxc://vorash.srv.dcmtl.stgraber.net | Add remote "vorash" pointing to a remote lxc instance using the full URI.
lxc remote set-default vorash                                   | Mark it as the default remote.
lxc start c1                                                    | Start container "c1" on it.

* * *

## publish

**Arguments**

    <resource> <target> [--public]

**Description**
Takes an existing container or container snapshot and makes a
compressed image out of it. By default the image will be private, that is,
it’ll only be accessible locally or remotely by authenticated clients. If --
public is passed, then anyone can pull the image so long as lxd is running.

It will also be possible for some image stores to allow users to push new
images to them using that command, though the two image stores that will come
pre-configured will be read-only.

**Examples**

Command                                         | Result
:------                                         | :-----
lxc publish c1/yesterday dakara:prod-images/c1  | Publish the "yesterday" snapshot of container "c1" as an image called "prod-images/c1" on remote host "dakara".
lxc publish c2 dakara:demo-images/c2 --public   | Publish local container "c2" as an image called "demo-images/c2" on remote host "dakara" and make it available to unauthenticated users.
