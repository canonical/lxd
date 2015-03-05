# Live Migration in LXD

## Overview

Migration has two pieces, a "source", that is, the host that already has the
container, and a "sink", the host that's getting the container. Currently,
in the 'pull' mode, the source sets up an operation, and the sink connects
to the source and pulls the container.

There are three websockets (channels) used in migration: 1. the control stream,
2. the criu images stream, and 3. the filesystem stream. When a migration is
initiated, information about the container, its configuration, etc. are sent
over the control channel (a full description of this process is below), the
criu images and container filesystem are synced over their respective channels,
and the result of the restore operation is sent from the sink to the source
over the control channel.

In particular, the protocol that is spoken over the criu channel and filesystem
channel can vary, depending on what is negotiated over the control socket. For
example, both the source and the sink's LXD directory is on btrfs, the
filesystem socket can speak btrfs-send/receive. Additionally, although we do a
"stop the world" type migration right now, support for criu's p.haul protocol
will happen over the criu socket.

## Control Socket

Once all three websockets are connected between the two endpoints, the source
sends a MigrationHeader (protobuf description found in
`/lxd/migration/migrate.proto`). This header contains the container
configuration which will be added to the new container (TODO: profiles?). There
are also two fields indicating the filesystem and criu protocol to speak. For
example, if a server is hosted on a btrfs filesystem, it can indicate that it
wants to do a `btrfs send` instead of a simple rsync (similarly, it could
indicate that it wants to speak the p.haul protocol, instead of just rsyncing
the images over slowly). The sink then examines this message and responds with
whatever it supports. Continuing our example, if the sink is not on a btrfs
filesystem, it responds with the lowest common denominator (rsync, in this
case), and the source is to send the root filesystem using rsync. Similarly
with the criu connection; if the sink doesn't have support for the p.haul
protocol (or whatever), we fall back to rsync.
