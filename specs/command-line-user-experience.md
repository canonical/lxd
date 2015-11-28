# Introduction

The "lxc" command is the main tool used by users to interact with LXD when
running it outside of OpenStack. The command is available to all users and can
be used to manage any local or remote resources provided they have the
credentials to do so.

# Remote operations

The lxc command line tool is designed to manage LXD hosts as well as connect
to a variety of remote resources.

The list of remote servers and their credentials is only stored in the client,
the servers don't know about each other nor do they have to. The client is the
one initiating any cross-server communication by instructing the two servers to
start talking to each other. Should that fail, for example because of a
firewall between the two servers, the client will then act as a relay
forwarding the data stream between the two servers.

* * *

# Resources
The lxc command interacts with resources. Currently supported resources are:
 * containers
 * container snapshots
 * images
 * container hosts

lxc defaults to interacting with the local LXD daemon, remote operations
must be prefixed with the remote server's name followed by a colon.

Some examples with the "info" command:

Command                               | Result
:------                               | :-----
lxc info                              | Show some information on the local LXD server
lxc info dakara:                      | Same but against the remote "dakara" server
lxc info c1                           | Show information about the "c1" container
lxc image info ubuntu/trusty/amd64    | Show information about the "ubuntu/trusty/amd64" image (alias)
lxc info dakara:c2/yesterday          | Show information about the "yesterday" snapshot of container "c2" on remote host "dakara"


This URI scheme is designed to be very specific (no ambiguity) and as short as
possible.

* * *

# Commands
## Overview

Command     | Description
:------     | :----------
config      | Change container settings (quotas, notes, OS metadata, ...)
copy        | Copy an existing container or container snapshot as a new container
delete      | Delete a resource (container, snapshot, image, ...)
exec        | Spawn a command within the container
file        | Transfer files in and out of the container
image       | Image handling
info        | Show information about a container, container snapshot or remote server
init        | Create a container without starting it
launch      | Create and start a new container from an image
list        | List all the containers
move        | Move a container either to rename it or to migrate it
profile     | Manage container configuration profiles.
publish     | Make an image out of an existing container or container snapshot
remote      | Remote server handling
restart     | Restart a container
restore     | Restore a snapshot of a container
snapshot    | Make a snapshot (stateful or not) of a container
start       | Start a container
stop        | Stop a container

* * *

## config

**Arguments**

    edit [resource]
    get [resource] <key>
    set [resource] <key> <value>
    show [resource]
    unset [resource] <key>
    device add <resource> <device name> <type> [key=value]...
    device remove <resource> <device name>
    device list <resource>
    device show <resource>
    trust add [remote] <certificate>
    trust remove [remote] <fingerprint>
    trust list [remote]

**Description**

Probably one of the most complex commands, it allows querying and
setting all the configuration options available for containers and LXD hosts.

The trust sub-command is there to manage the server's trust store. It
can list the certificates which the server currently trusts, delete
entries (based on their fingerprint) and add new entries using a
provided certificate.

The edit commands are there to offer a more convenient user interface by
opening a text editor in which the current configuration is displayed
alongside a set of useful examples. The user can then edit things in
place and when saved, all changes will be committed.

**Examples**

Command                                                                         | Result
:------                                                                         | :-----
lxc config show                                                                 | Show the local server's configuration
lxc config show dakara:                                                         | Show "dakara"'s server' configuration
lxc config set core.trust\_password new-trust-password                          | Set the local server's trust password to "new-trust-password"
lxc config set c1 limits.memory 2G                                              | Set a memory limit of 2GB for container "c1"
lxc config show c1                                                              | Show the configuration of the "c1" container, starting by the list of profiles it’s based on, then the container specific settings and finally the resulting overall configuration.
lxc config trust add new-client-cert.crt                                        | Add new-client-cert.pem to the default remote's trust store (typically local LXD)
lxc config trust add dakara: new-client-cert.crt                                | Add new-client-cert.pem to the "dakara"'s trust store
lxc config trust list                                                           | List all the trusted certificates on the default remote
lxc config trust list dakara:                                                   | List all the trusted certificates on "dakara"
lxc config trust remove [name|\<cert fingerprint\>]                             | Remove a certificate from the default remote
lxc config trust remove dakara: \<cert fingerprint\>                            | Remove a certificate from "dakara"'s trust store

* * *

## copy

**Arguments**

    <source container/snapshot> [container name]

**Description**

Creates a copy of an existing container or container snapshot as a new
container. If the new container's name isn't specified, a random one
will be generated.

**Examples**

Command                                 | Result
:------                                 | :-----
lxc copy c1 c2                          | Create a container called "c2" which is a copy of container "c1" with its hostname changed and a fresh MAC address
lxc copy c1 dakara:                     | Copy container "c1" to remote host "dakara" still keeping the name "c1" on the target
lxc copy c1 dakara:c2                   | Same as above but also rename the container and change its hostname


* * *

## delete

**Arguments**

    <container or snapshot name>

**Description**
Destroy a container or container snapshot and any attached data
(configuration, snapshots, ...).

This will destroy the resource (container) even if it is currently in use.

**Examples**

Command                         | Result
:------                         | :-----
lxc delete c1                   | Remove the c1 container, its configuration and any snapshot it may have
lxc delete c1/yesterday         | Remove the "yesterday" snapshot of "c1"
lxc delete dakara:c2/yesterday  | Remove the "yesterday" snapshot for "c2" on remote host "dakara"

* * *

## exec

**Arguments**

    <container> command...

**Description**

Execute a command inside the remote container.

**Examples**

Command                                                 | Result
:------                                                 | :-----
lxc exec c1 -- /bin/bash                                   | Spawn /bin/bash in local container c1
tar cf - /opt/myapp \| lxc exec dakara:c2 -- tar xvf -    | Make a tarball of /opt/myapp with the stream going out to stdout, then have that piped into lxc exec connecting to a receiving tar command in container running on remote host "dakara"

* * *

## file

**Arguments**

    file push [--uid=UID] [--gid=GID] [--mode=MODE] <source> [<source>...] <target>
    file pull <source> [<source>...] <target>

**Description**
Copies file to or from the container. Supports rewriting the uid/gid/mode. This
is only allowed for containers that are currently running.

**Examples**

Command                                                 | Result
:------                                                 | :-----
lxc file push --uid=0 --gid=0 test.sh dakara:c2/root/   | Push test.sh as /root/test.sh inside container "c2" on host "dakara", rewrite the uid/gid to 0/0
lxc file pull dakara:c2/etc/hosts /tmp/                 | Grab /etc/hosts from container "c2" on "dakara" and write it as /tmp/hosts on the client

* * *

## image

**Arguments**

    image alias create <alias> <target>
    image alias list [<remote>:]
    image alias delete <alias>
    image copy [<remote>:]<image> <remote>: [--alias=ALIAS].. [--copy-aliases] [--public]
    image delete <image>
    image edit <image>
    image export <image> [target]
    image import <tarball> [rootfs tarball] [target] [--public] [--created-at=ISO-8601] [--expires-at=ISO-8601] [--fingerprint=FINGERPRINT] [--alias=ALIAS].. [prop=value]
    image info <image>
    image list [filter]
    image move <image> <remote:>
    image set <image> <key> <value>
    image show <image>
    image unset <image> <key>

**Description**
Manage the LXD image store.

Images can either be fed from an external tool using the API or manually
imported into LXD using the import command. Attributes can then be set
on them and images can be copied/moved to other LXD hosts.

Images may also be copied or moved between hosts.

The unique identifier of an image is its sha256, as a result, it's only
possible to have one copy of any given image on a given LXD host.


The "description" property is special in that if it's set, it'll appear in "lxc image list".

Aliases are mappings between a user friendly name and an image.
Aliases may contain any character except for colons.

Images are typically referenced by their full or partial fingerprint, in most
cases aliases may also be used and for listings, property filters can
also be used.


**Examples**

Command                                                                                                                 | Result
:------                                                                                                                 | :-----
lxc image import centos-7-x86\_64.tar.gz --created-at=2014-12-10 --expires-at=2015-01-10 os=fedora release=7 arch=amd64 | Import a centos LXD image in the local LXD image store
lxc image import debian-jessie\_amd64.tar.gz dakara:                                                                    | Import a debian LXD image in the lxc image store of remote host "dakara"
lxc image import debian-jessie\_amd64.meta.tar.gz debian-jessie\_amd64.tar.g dakara:                                    | Import a debian LXD image in split format in the lxc image store of remote host "dakara"
lxc image alias create centos/7 \<fingerprint\>                                                                         | Create an alias for centos/7 pointing to our centos 7 image

**Example output (lxc image list)**

    ALIAS                   FINGERPRINT     PUBLIC  DESCRIPTION                     UPLOAD DATE
    -------------------------------------------------------------------------------------------
    busybox-amd64           146246146827... yes     -                               Mar 12, 2015 at 10:41pm (CDT)
    ubuntu/devel (3 more)   95830b5e4e04... yes     Ubuntu 15.04 (devel) x86 64bit  Mar 8, 2015 at 1:27am (CST)
    -                       a1420943168a... no      Test image                      Mar 4, 2015 at 3:41pm (CST)

**Example output (lxc image info)**

    Fingerprint: 146246146827e213eff5c9b5243c8c28cf461184a507588d6c7abac192e600dd
    Filename: ubuntu-vivid-amd64-default-20150308.tar.xz
    Size: 65MB
    Architecture: x86_64
    Public: yes
    Timestamps:
        Created: 2015/03/08 10:50 UTC
        Uploaded: 2015/03/09 16:00 UTC
        Expires: never
    Properties:
        arch: x86_64
        build: 20150308
        description: Ubuntu 15.04 (devel) x86 64bit
        os: Ubuntu
        release: vivid, 15.04
        variant: default
    Aliases:
        - ubuntu/devel
        - ubuntu/vivid
        - ubuntu/vivid/amd64

* * *

## info

**Arguments**

    [resource]

**Description**

Prints information about a container, snapshot or LXD host.

**Examples**

Command                                       | Result
:------                                       | :-----
lxc info                                      | Displays local host status
lxc info dakara:                              | Displays the host status of remote host "dakara"
lxc info c1                                   | Displays the status of local container "c1"
lxc info dakara:c2                            | Displays the status of remote container "c2" on "dakara"
lxc info dakara:c2/yesterday                  | Displays the status of snapshot "yesterday" for remote container "c2" on "dakara"

* * *

## init

**Arguments**

    <image> [container name] [--ephemeral|-e] [--profile|-p <profile>...]

**Description**

init is used to create a new container from an image, but not start it.

If the container name isn't specified, a random one will be used.

Passing --ephemeral will make LXD create a temporary container which
will be destroyed when shutdown.

--profile is used to apply a configuration profile (or multiple ones if passed
multiple times) to the newly created container, when passed with an existing
container, it will only append the configuration profile for that run.

**Examples**

Command                                        | Result
:------                                        | :-----
lxc init ubuntu/trusty/amd64                   | Create a new local container based on the Ubuntu 14.04 amd64 image and with a random name
lxc init ubuntu/precise/i386 dakara:           | Create a new remote container on "dakara" based on the local Ubuntu 14.04 i386 image and with a random name
lxc init ubuntu c1 -p micro                    | Create a new local container called "c1" based on the Ubuntu image and run it with a "micro" profile

* * *

## launch

**Arguments**

    <image name> [container name] [--ephemeral|-e] [--profile|-p <profile>...]

**Description**

launch is used to create and start a new container from an image.

If the container name isn't specified, a random one will be used.

Passing --ephemeral will make LXD create a temporary container which
will be destroyed when shutdown.

--profile is used to apply a configuration profile (or multiple ones if passed
multiple times) to the newly created container, when passed with an existing
container, it will only append the configuration profile for that run.

**Examples**

Command                                         | Result
:------                                         | :-----
lxc launch ubuntu/trusty/amd64                  | Create a new local container using a random name and based on the Ubuntu 14.04 amd64 image
lxc launch ubuntu/precise/i386 dakara:          | Create a new remote container on "dakara" using a random name and based on the local Ubuntu 14.04 i386 image
lxc launch ubuntu c1 -p with-nesting            | Create a new local container called "c1" based on the Ubuntu image and run it with a profile allowing container nesting

## list
**Arguments**

    [resource] [filters] [format]

**Description**

Lists all the available containers. If a container is specified, then
it'll list all the available snapshots for the container.

Each comes with some minimal status information (status, addresses, ...)
configurable if needed by passing a list of fields to display.

For containers, a reasonable default would be to show the name, state, ipv4
addresses, ipv6 addresses, memory and disk consumption.
Snapshots would be displayed below their parent containers and would re-use the
name, state and disk consumption field, the others wouldn’t be relevant and
will be displayed as "-".

The filters are:
 * A single keyword like "web" which will list any container with "web" in its name.
 * A key/value pair referring to a configuration item. For those, the namespace can be abreviated to the smallest unambiguous identifier:
   * "user.blah=abc" will list all containers with the "blah" user property set to "abc"
   * "u.blah=abc" will do the same
   * "security.privileged=1" will list all privileged containers
   * "s.privileged=1" will do the same

Multiple filters may be passed, a container will then have to match them all to be listed.

**Examples**

Command             | Result
:------             | :-----
lxc list            | Show the list of local containers, snapshots and images
lxc list dakara:    | Show the list of remote containers, snapshots and images on "dakara"
lxc list c1         | Show the entry for the local container "c1" as well as any snapshot it may have

**Example output**

    NAME         STATE    IPV4       IPV6                                    MEMORY     DISK
    -------------------------------------------------------------------------------------------
    precise      STOPPED  -          -                                       -          UNKNOWN
    precise-gui  RUNNING  10.0.3.59  2607:f2c0:f00f:2761:216:3eff:fe51:234f  4435.89MB  UNKNOWN
    vivid        STOPPED  -          -                                       -          UNKNOWN

* * *

## move

**Arguments**

    <source resource> <destination resource>

**Description**

Moves a resource either locally (rename) or remotely (migration). If the
container is running, this will do a live migration, otherwise it will simply
move the on-disk container data.

**Examples**

Command                         | Result
:------                         | :-----
lxc move c1 c2                  | Rename container c1 to c2
lxc move c1 dakara:             | Move c1 to "dakara". If the container is stopped, this simply moves the container and its configuration to "dakara". If it is running, this live migrates container c1 to "dakara". This will first stream the filesystem content over to "dakara", then dump the container state to disk, sync the state and the delta of the filesystem, restore the container on the remote host and then wipe it from the source host
lxc move c1 dakara:c2           | Move c1 to "dakara" as "c2"

* * *

## profile

**Arguments**

    device add <profile name> <device name> <type> [key=value]...
    device remove <profile name> <device name>
    device list <profile name>
    apply <resource> <profile name>[,<second profile name>, ...]
    create <profile name>
    copy <source profile name> <target profile name>
    delete <profile name>
    edit <profile name>
    list [remote] [filters]
    get <profile name> <key>
    move <profile name> <new profile name>
    set <profile name> <key> <value>
    show <profile name>
    unset <profile name> <key>

This command supports profiles which are used to group configuration settings
(configurations keys and devices) and then apply the resulting set to a given
container.

It’s possible to create, delete, list and setup profiles on a remote
host. The one limitation is that a container can only reference local
profiles, so profiles need to be copied across hosts or be moved around
alongside the containers.

Also note that removing a profile or moving it off the host will fail if any
local container still references it.

**Examples**

Command                                                                  | Result
:------                                                                  | :-----
lxc profile create micro                                                 | Create a new "micro" profile.
lxc profile set micro limits.memory 256M                                 | Restrict memory usage to 256MB
lxc profile set micro limits.cpu 1                                      | Restrict CPU usage to a single core
lxc profile copy micro dakara:                                           | Copy the resulting profile over to "dakara"
lxc profile show micro                                                   | Show all the options associated with the "micro" profile and all the containers using it
lxc profile unset dakara:nano limits.memory                              | Unset "limits.memory" for the "nano" profile on "dakara"
lxc profile apply c1 micro,nesting                                       | Set the profiles for container "c1" to be "micro" followed by "nesting"
lxc profile apply c1 ""                                                  | Unset any assigned profile for container "c1"


* * *

## publish

**Arguments**

    <resource> [target] [--public] [--expires-at=ISO-8601] [--alias=ALIAS].. [prop-key=prop-value]...

**Description**
Takes an existing container or container snapshot and makes a
compressed image out of it. By default the image will be private, that is,
it’ll only be accessible locally or remotely by authenticated clients. If --
public is passed, then anyone can pull the image so long as LXD is running.

It will also be possible for some image stores to allow users to push new
images to them using that command, though the two image stores that will come
pre-configured will be read-only.

**Examples**

Command                                         | Result
:------                                         | :-----
lxc publish c1/yesterday                        | Turn c1/yesterday into a private image for consumption by trusted LXD servers
lxc publish c2 dakara: --public                 | Turn c2 into a public image on remote host "dakara"

* * *

## remote

**Arguments**

    add <name> <URI> [--always-relay] [--password=PASSWORD] [--accept-certificate] [--public]
    remove <name>
    list
    rename <old name> <new name>
    set-url <name> <new URI>
    set-default <name>
    get-default

**Description**
Manages remote LXD servers.

Scheme                  | Description
:-----                  | :----------
unix://Unix             | socket (or abstract if leading @) access to LXD
https://                | Communication with LXD over the network (https)

By default lxc will have the following remotes defined:

Name        | URI                                               | Description
:---        | :--                                               | :----------
local       | unix:///var/lib/lxd/sock                          | Communication to the local LXD daemon (hidden if not present)

The default remote is "local", this allows simple operations with local
resources without having to specify local: in front of all of their names. This
behavior can be changed by using the set-default argument. On a system without
a local LXD, the first manually added remote should be automatically set as
default.

Protocol auto-detection will happen so that adding a source solely based on its
name will work too, assuming it doesn’t support multiple protocols.

The "--always-relay" flag of "remote add" can mean one of two things:
 * If it's an image server, that this server is only reachable by the
   client and that the client needs to act as a relay and transfer the
   image over to the server.
 * If it's a LXD server, that this server has limited connectivity which
   prevents it from accessing the image servers and that the client needs
   to act as a relay for it.

The "--accept-certificate" flag of "remote add" will automatically accept
the remote's certificate without prompting the user to verify the certificate
fingerprint.

**Examples**

Command                                                                  | Result
:------                                                                  | :-----
lxc remote add dakara dakara.local                                       | Add a new remote called "dakara" using its avahi DNS record and protocol auto-detection
lxc remote add dakara dakara.local --password=BLAH                       | Add a new remote called "dakara" using its avahi DNS record and protocol auto-detection and providing the password in advance
lxc remote add dakara dakara.local --password=BLAH --accept-certificate  | Add a new remote called "dakara" using its avahi DNS record and protocol auto-detection and providing the password in advance and also accepting the certificate without fingerprint verification
lxc remote add vorash https://vorash.srv.dcmtl.stgraber.net              | Add remote "vorash" pointing to a remote lxc instance using the full URI
lxc remote set-default vorash                                            | Mark it as the default remote
lxc start c1                                                             | Start container "c1" on it

* * *

## restart

**Arguments**

    <resource> [--kill|-k] [--timeout|-t]

**Description**

Restarts the container. The flags have the same behavior as the 'stop' command.
Restart will fail on ephemeral containers, as they cannot be booted after they
are stopped.

**Examples**

Command                     | Result
:------                     | :-----
lxc restart c1              | Do a clean restart of local container "c1"
lxc restart dakara:c1 -t 10 | Do a clean restart of remote container "c1" on "dakara" with a reduced timeout of 10s
lxc restart dakara:c1 -k    | Kill and restart the remote container "c1" on "dakara"
 
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
lxc restore c1 it-works                         | Restore the c1 container back to its "it-works" snapshot state
lxc restore dakara:c1 pre-upgrade --stateful    | Restore a pre-dist-upgrade snapshot of container "c1" running on "dakara". Allows for a very fast recovery time in case of problem

* * *

## snapshot

**Arguments**

    <resource> [snapshot name] [--stateful] [--expire=ISO-8601]

**Description**

Makes a read-only snapshot of a resource (typically a container).
For a container this will be a snapshot of the container’s filesystem,
configuration and if --stateful is passed, its current running state.

If the snapshot name isn't specified, a timestamp will be used.

**Examples**

Command                                         | Result
:------                                         | :-----
lxc snapshot c1 it-works                        | Create "it-works" snapshot of container c1
lxc snapshot dakara:c1 pre-upgrade --stateful   | Make a pre-dist-upgrade snapshot of container "c1" running on "dakara". Allows for a very fast recovery time in case of problem

* * *

## start

**Arguments**

    <resource>

**Description**

start is used to start an existing container.


**Examples**

Command                                        | Result
:------                                        | :-----
lxc start c2                                   | Start local container "c2"
lxc start dakara:c3                            | Start the "c3" container on remote host "dakara"

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
