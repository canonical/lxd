# Introduction

This specification covers some of the daemon's behavior, such as reaction to given signals, crashes, ...

# Startup
On every start, LXD checks that its directory structure exists. If it
doesn't, it'll create the required directories, generate a keypair and
initialize the database.

Once the daemon is ready for work, LXD will scan the containers table
for any container for which the stored power\_state differs from the
current one. If a container's power\_state in the database is 1 and the
container isn't running, LXD will start it.

# Signal handling
## SIGINT, SIGQUIT, SIGTERM
For those signals, LXD assumes that it's being temporarily stopped and
will be restarted at a later time to continue handling the containers.

The containers will keep running and LXD will close all connections and
exit cleanly.

## SIGPWR
Indicates to LXD that the host is going down.

LXD will attempt a clean shutdown of all the containers. After 30s, it
will kill any remaining container.

The container power\_state in the containers table is kept as it was so
that LXD after the host is done rebooting can restore the containers as
they were.

## SIGUSR1
Write a memory profile dump to the file specified with \-\-memprofile.
