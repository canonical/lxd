# Daemon behavior

## Introduction

This specification covers some of the daemon's behavior, such as
reaction to given signals, crashes, ...

## Startup

On every start, LXD checks that its directory structure exists. If it
doesn't, it'll create the required directories, generate a key pair and
initialize the database.

Once the daemon is ready for work, LXD will scan the instances table
for any instance for which the stored power state differs from the
current one. If an instance's power state was recorded as running and the
instance isn't running, LXD will start it.

## Signal handling

### `SIGINT`, `SIGQUIT`, `SIGTERM`

For those signals, LXD assumes that it's being temporarily stopped and
will be restarted at a later time to continue handling the instances.

The instances will keep running and LXD will close all connections and
exit cleanly.

### `SIGPWR`

Indicates to LXD that the host is going down.

LXD will attempt a clean shutdown of all the instances. After 30s, it
will kill any remaining instance.

The instance `power_state` in the instances table is kept as it was so
that LXD after the host is done rebooting can restore the instances as
they were.

### `SIGUSR1`

Write a memory profile dump to the file specified with `--memprofile`.
