# Database

## Introduction
So first of all, why a database?

Rather than keeping the configuration and state within each container's
directory as is traditionally done by LXC, LXD has an internal database
which stores all of that information. This allows very quick queries
against all containers configuration.


An example is the rather obvious question "what containers are using br0?".
To answer that question without a database, LXD would have to iterate
through every single container, load and parse its configuration and
then look at what network devices are defined in there.

While that may be quick with a few containers, imagine how many
filesystem access would be required for 2000 containers. Instead with a
database, it's only a matter of accessing the already cached database
with a pretty simple query.


## Database engine
Since LXD supports clustering, and all members of the cluster must share the
same database state, the database engine is based on a [distributed
version](https://github.com/CanonicalLtd/dqlite) of SQLite, which provides
replication, fault-tolerance and automatic failover without the need of external
database processes. We refer to this database as the "global" LXD database.

Even when using LXD as single non-clustered node, the global database will still
be used, although in that case it effectively behaves like a regular SQLite
database.

The files of the global database are stored under the ``./database/global``
sub-directory of your LXD data dir (e.g. ``/var/lib/lxd/database/global``).

Since each member of the cluster also needs to keep some data which is specific
to that member, LXD also uses a plain SQLite database (the "local" database),
which you can find in ``./database/local.db``.

Backups of the global database directory and of the local database file are made
before upgrades, and are tagged with the ``.bak`` suffix. You can use those if
you need to revert the state as it was before the upgrade.

## Dumping the database content or schema
If you want to get a SQL text dump of the content or the schema of the databases,
use the ``lxd sql <local|global> [.dump|.schema]`` command, which produces the
equivalent output of the ``.dump`` or ``.schema`` directives of the sqlite3
command line tool.

## Running custom queries from the console
If you need to perform SQL queries (e.g. ``SELECT``, ``INSERT``, ``UPDATE``)
against the local or global database, you can use the ``lxd sql`` command (run
``lxd sql --help`` for details).

You should only need to do that in order to recover from broken updates or bugs.
Please consult the LXD team first (creating a [GitHub
issue](https://github.com/lxc/lxd/issues/new) or
[forum](https://discuss.linuxcontainers.org/) post).

## Running custom queries at LXD daemon startup
In case the LXD daemon fails to start after an upgrade because of SQL data
migration bugs or similar problems, it's possible to recover the situation by
creating ``.sql`` files containing queries that repair the broken update.

To perform repairs against the local database, write a
``./database/patch.local.sql`` file containing the relevant queries, and
similarly a ``./database/patch.global.sql`` for global database repairs.

Those files will be loaded very early in the daemon startup sequence and deleted
if the queries were successful (if they fail, no state will change as they are
run in a SQL transaction).

As above, please consult the LXD team first.

## Syncing the cluster database to disk
If you want to flush the content of the cluster database to disk, use the ``lxd
sql global .sync`` command, that will write a plain SQLite database file into
``./database/global/db.bin``, which you can then inspect with the ``sqlite3``
command line tool.
