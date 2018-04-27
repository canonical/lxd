# Introduction
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


# Database engine
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

# Design
The design of the database is made to be as close as possible to
the [RESTful API](rest-api.md).

The main table and field names are exact match for the REST API.

However this database isn't an exact match of the API, mostly because
any runtime or external piece of information will not be stored in the
database (as this would require constent polling and wouldn't gain us
anything).

We make no guarantee of stability for the database schema. This is a
purely internal database which only LXD should ever use. Updating LXD
may cause a schema update and data being shuffled. In those cases, LXD
will make a copy of the old database as ".old" to allow for a revert.


# Tables
The list of tables is:

 * certificates
 * config
 * containers
 * containers\_config
 * containers\_devices
 * containers\_devices\_config
 * containers\_profiles
 * images
 * images\_aliases
 * images\_properties
 * images\_source
 * networks
 * networks\_config
 * patches
 * profiles
 * profiles\_config
 * profiles\_devices
 * profiles\_devices\_config
 * schema

You'll notice that compared to the REST API, there are a few differences:

 1. The extra "\*\_config" tables which are there for key/value config storage.
 2. The extra "images\_properties" table which is there for key/value property storage.
 3. The extra "schema" table whish is used for database schema version tracking.
 4. The extra "patches" table used for data migration and other non-schema changes on upgrades.
 5. There is no "snapshots" table. That's because snapshots are a copy
    of a container at a given point in time, including its configuration and
    on-disk state. So having snapshots in a separate table would only be needless duplication.

# Notes on sqlite3
sqlite3 only supports 5 storage classes: NULL, INTEGER, REAL, TEXT and BLOB
There are then a set of aliases for each of those storage classes which is what we use below.

# Schema
## certificates

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
fingerprint     | VARCHAR(255)  | -             | NOT NULL          | HEX encoded certificate fingerprint
type            | INTEGER       | -             | NOT NULL          | Certificate type (0 = client)
name            | VARCHAR(255)  | -             | NOT NULL          | Certificate name (defaults to CN)
certificate     | TEXT          | -             | NOT NULL          | PEM encoded certificate

Index: UNIQUE ON id AND fingerprint


## config (server configuration)

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
key             | VARCHAR(255)  | -             | NOT NULL          | Configuration key
value           | TEXT          | -             |                   | Configuration value (NULL for unset)

Index: UNIQUE ON id AND key


## containers

Column            | Type          | Default       | Constraint        | Description
:-----            | :---          | :------       | :---------        | :----------
id                | INTEGER       | SERIAL        | NOT NULL          | SERIAL
name              | VARCHAR(255)  | -             | NOT NULL          | Container name
architecture      | INTEGER       | -             | NOT NULL          | Container architecture
type              | INTEGER       | 0             | NOT NULL          | Container type (0 = container, 1 = container snapshot)
ephemeral         | INTEGER       | 0             | NOT NULL          | Whether the container is ephemeral (0 = persistent, 1 = ephemeral)
stateful          | INTEGER       | 0             | NOT NULL          | Whether the snapshot contains state (snapshot only)
creation\_date    | DATETIME      | -             |                   | Container creation date
last\_use\_date   | DATETIME      | -             |                   | Last container action

Index: UNIQUE ON id AND name


## containers\_config

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
container\_id   | INTEGER       | -             | NOT NULL          | containers.id FK
key             | VARCHAR(255)  | -             | NOT NULL          | Configuration key
value           | TEXT          | -             |                   | Configuration value (NULL for unset)

Index: UNIQUE ON id AND container\_id + key

Foreign keys: container\_id REFERENCES containers(id)


## containers\_devices

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
container\_id   | INTEGER       | -             | NOT NULL          | containers.id FK
name            | VARCHAR(255)  | -             | NOT NULL          | Container name
type            | INTEGER       | 0             | NOT NULL          | Device type (see configuration.md)

Index: UNIQUE ON id AND container\_id + name

Foreign keys: container\_id REFERENCES containers(id)


## containers\_devices\_config

Column                  | Type          | Default       | Constraint        | Description
:-----                  | :---          | :------       | :---------        | :----------
id                      | INTEGER       | SERIAL        | NOT NULL          | SERIAL
container\_device\_id   | INTEGER       | -             | NOT NULL          | containers\_devices.id FK
key                     | VARCHAR(255)  | -             | NOT NULL          | Configuration key
value                   | TEXT          | -             |                   | Configuration value (NULL for unset)

Index: UNIQUE ON id AND container\_device\_id + key

Foreign keys: container\_device\_id REFERENCES containers\_devices(id)


## containers\_profiles

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
container\_id   | INTEGER       | -             | NOT NULL          | containers.id FK
profile\_id     | INTEGER       | -             | NOT NULL          | profiles.id FK
apply\_order    | INTEGER       | 0             | NOT NULL          | Profile ordering

Index: UNIQUE ON id AND container\_id + profile\_id

Foreign keys: container\_id REFERENCES containers(id) and profile\_id REFERENCES profiles(id)


## images

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
cached          | INTEGER       | 0             | NOT NULL          | Whether this is a cached image
fingerprint     | VARCHAR(255)  | -             | NOT NULL          | Tarball fingerprint
filename        | VARCHAR(255)  | -             | NOT NULL          | Tarball filename
size            | INTEGER       | -             | NOT NULL          | Tarball size
public          | INTEGER       | 0             | NOT NULL          | Whether the image is public or not
auto\_update    | INTEGER       | 0             | NOT NULL          | Whether to update from the source of this image
architecture    | INTEGER       | -             | NOT NULL          | Image architecture
creation\_date  | DATETIME      | -             |                   | Image creation date (user supplied, 0 = unknown)
expiry\_date    | DATETIME      | -             |                   | Image expiry (user supplied, 0 = never)
upload\_date    | DATETIME      | -             | NOT NULL          | Image entry creation date
last\_use\_date | DATETIME      | -             |                   | Last time the image was used to spawn a container

Index: UNIQUE ON id AND fingerprint


## images\_aliases

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
name            | VARCHAR(255)  | -             | NOT NULL          | Alias name
image\_id       | INTEGER       | -             | NOT NULL          | images.id FK
description     | VARCHAR(255)  | -             |                   | Description of the alias

Index: UNIQUE ON id AND name

Foreign keys: image\_id REFERENCES images(id)


## images\_properties

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
image\_id       | INTEGER       | -             | NOT NULL          | images.id FK
type            | INTEGER       | 0             | NOT NULL          | Property type (0 = string, 1 = text)
key             | VARCHAR(255)  | -             | NOT NULL          | Property name
value           | TEXT          | -             |                   | Property value (NULL for unset)

Index: UNIQUE ON id

Foreign keys: image\_id REFERENCES images(id)

## images\_source

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
image\_id       | INTEGER       | -             | NOT NULL          | images.id FK
server          | TEXT          | -             | NOT NULL          | Server URL
protocol        | INTEGER       | 0             | NOT NULL          | Protocol to access the remote (0 = lxd, 1 = direct, 2 = simplestreams)
certificate     | TEXT          | -             |                   | PEM encoded certificate of the server
alias           | VARCHAR(255)  | -             | NOT NULL          | What remote alias to use as the source

Index: UNIQUE ON id

Foreign keys: image\_id REFERENCES images(id)

## networks

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
name            | VARCHAR(255)  | -             | NOT NULL          | Profile name

Index: UNIQUE on id AND name

## networks\_config

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
network\_id     | INTEGER       | -             | NOT NULL          | networks.id FK
key             | VARCHAR(255)  | -             | NOT NULL          | Configuration key
value           | TEXT          | -             |                   | Configuration value (NULL for unset)

Index: UNIQUE ON id AND network\_id + key

Foreign keys: network\_id REFERENCES networks(id)

## patches

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
name            | VARCHAR(255)  | -             | NOT NULL          | Patch name
applied\_at     | DATETIME      | -             | NOT NULL          | When the patch was applied

Index: UNIQUE ON id AND name

## profiles

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
name            | VARCHAR(255)  | -             | NOT NULL          | Profile name
description     | TEXT          | -             |                   | Description of the profile

Index: UNIQUE on id AND name


## profiles\_config

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
profile\_id     | INTEGER       | -             | NOT NULL          | profiles.id FK
key             | VARCHAR(255)  | -             | NOT NULL          | Configuration key
value           | VARCHAR(255)  | -             |                   | Configuration value (NULL for unset)

Index: UNIQUE ON id AND profile\_id + key

Foreign keys: profile\_id REFERENCES profiles(id)


## profiles\_devices

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
profile\_id     | INTEGER       | -             | NOT NULL          | profiles.id FK
name            | VARCHAR(255)  | -             | NOT NULL          | Container name
type            | INTEGER       | 0             | NOT NULL          | Device type (see configuration.md)

Index: UNIQUE ON id AND profile\_id + name

Foreign keys: profile\_id REFERENCES profiles(id)


## profiles\_devices\_config

Column                  | Type          | Default       | Constraint        | Description
:-----                  | :---          | :------       | :---------        | :----------
id                      | INTEGER       | SERIAL        | NOT NULL          | SERIAL
profile\_device\_id     | INTEGER       | -             | NOT NULL          | profiles\_devices.id FK
key                     | VARCHAR(255)  | -             | NOT NULL          | Configuration key
value                   | TEXT          | -             |                   | Configuration value (NULL for unset)

Index: UNIQUE ON id AND profile\_device\_id + key

Foreign keys: profile\_device\_id REFERENCES profiles\_devices(id)


## schema

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
version         | INTEGER       | -             | NOT NULL          | Schema version
updated\_at     | DATETIME      | -             | NOT NULL          | When the schema update was done

Index: UNIQUE ON id AND version

## storage\_pools

Column                  | Type          | Default       | Constraint        | Description
:-----                  | :---          | :------       | :---------        | :----------
id                      | INTEGER       | SERIAL        | NOT NULL          | SERIAL
name                    | VARCHAR(255)  | -             | NOT NULL          | storage pool name
driver                  | VARCHAR(255)  | -             | NOT NULL          | storage pool driver

## storage\_pools\_config

Column                  | Type          | Default       | Constraint        | Description
:-----                  | :---          | :------       | :---------        | :----------
id                      | INTEGER       | SERIAL        | NOT NULL          | SERIAL
storage\_pool\_id       | INTEGER       | -             | NOT NULL          | storage\_pools.id FK
key                     | VARCHAR(255)  | -             | NOT NULL          | Configuration key
value                   | TEXT          | -             |                   | Configuration value (NULL for unset)

## storage\_volumes

Column                  | Type          | Default       | Constraint        | Description
:-----                  | :---          | :------       | :---------        | :----------
id                      | INTEGER       | SERIAL        | NOT NULL          | SERIAL
storage\_pool\_id       | INTEGER       | -             | NOT NULL          | storage\_pools.id FK
name                    | VARCHAR(255)  | -             | NOT NULL          | storage volume name
type                    | INTEGER       | -             | NOT NULL          | storage volume type

## storage\_volumes\_config

Column                  | Type          | Default       | Constraint        | Description
:-----                  | :---          | :------       | :---------        | :----------
id                      | INTEGER       | SERIAL        | NOT NULL          | SERIAL
storage\_volume\_id     | INTEGER       | -             | NOT NULL          | storage\_volumes.id FK
key                     | VARCHAR(255)  | -             | NOT NULL          | Configuration key
value                   | TEXT          | -             |                   | Configuration value (NULL for unset)
