---
relatedlinks: "[Canonical&#32;Dqlite](https://canonical.com/dqlite), [Dqlite&#32;GitHub](https://github.com/canonical/dqlite)"
---

(database)=
# The LXD Dqlite database

LXD uses a distributed database to store the server configuration and state, which allows for quicker queries than if the configuration was stored inside each instance's directory (as it is done by LXC, for example).

To understand the advantages, consider a query against the configuration of all instances, like "what instances are using `br0`?".
To answer that question without a database, you would have to iterate through every single instance, load and parse its configuration, and then check which network devices are defined in there.
With a database, you can run a simple query on the database to retrieve this information.

## Dqlite

In a LXD cluster, all members of the cluster must share the same database state.
Therefore, LXD uses [Dqlite](https://canonical.com/dqlite), a distributed version of SQLite.
Dqlite provides replication, fault-tolerance, and automatic failover without the need of external database processes.

When using LXD as a single machine and not as a cluster, the Dqlite database effectively behaves like a regular SQLite database.

(database-location)=
## File location

The database files are stored in the `database` sub-directory of your LXD data directory (thus `/var/snap/lxd/common/lxd/database/` if you use the snap, or `/var/lib/lxd/database/` otherwise).

Upgrading LXD to a newer version might require updating the database schema.
In this case, LXD automatically stores a backup of the database and then runs the update.
See {ref}`ref-snap-database` for more information.

## Backup

See {ref}`backup-database` for instructions on how to back up the contents of the LXD database.
