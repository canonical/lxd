---
relatedlinks: https://github.com/canonical/dqlite
---

# About the LXD database

## Introduction

So first of all, why a database?

Rather than keeping the configuration and state within each instance's
directory as is traditionally done by LXC, LXD has an internal database
which stores all of that information. This allows very quick queries
against all instances configuration.

An example is the rather obvious question "what instances are using `br0`?".
To answer that question without a database, LXD would have to iterate
through every single instance, load and parse its configuration and
then look at what network devices are defined in there.

While that may be quick with a few instance, imagine how many
file system access would be required for 2000 instances. Instead with a
database, it's only a matter of accessing the already cached database
with a pretty simple query.

## Database engine

Since LXD supports clustering, and all members of the cluster must share the
same database state, the database engine is based on a [distributed
version](https://github.com/canonical/dqlite) of SQLite, which provides
replication, fault-tolerance and automatic failover without the need of external
database processes. We refer to this database as the "global" LXD database.

Even when using LXD as single non-clustered node, the global database will still
be used, although in that case it effectively behaves like a regular SQLite
database.

Backups of the global database directory and of the local database file are made
before upgrades.
