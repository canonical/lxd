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
As this is a purely internal database with a single client and very
little data, we'll be using sqlite3.

We have no interest in replication or other HA features offered by the
bigger database engines as LXD runs on each compute nodes and having the
database accessible when the compute node itself isn't, wouldn't be
terribly useful.


# Design
The design of the database is made to be as close as possible to the REST API.

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
 * images\_properties
 * images\_aliases
 * profiles
 * profiles\_config
 * profiles\_devices
 * profiles\_devices\_config
 * schema

You'll notice that compared to the REST API, there are three main differences:

 1. The extra "\*\_config" tables which are there for key/value config storage.
 2. The extra "images\_properties" table which is there for key/value property storage.
 3. The extra "schema" table whish is used for database schema version tracking.
 4. There is no "snapshots" table. That's because snapshots are a copy
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

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
name            | VARCHAR(255)  | -             | NOT NULL          | Container name
architecture    | INTEGER       | -             | NOT NULL          | Container architecture
type            | INTEGER       | 0             | NOT NULL          | Container type (0 = container, 1 = container snapshot)
power\_state    | INTEGER       | 0             | NOT NULL          | Container power state (0 = off, 1 = on)
ephemeral       | INTEGER       | 0             | NOT NULL          | Whether the container is ephemeral (0 = persistent, 1 = ephemeral)

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
type            | INTEGER       | 0             | NOT NULL          | Container type (see configuration.md)

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
architecture    | INTEGER       | -             | NOT NULL          | Image architecture
creation\_date  | DATETIME      | -             |                   | Image creation date (user supplied, 0 = unknown)
expiry\_date    | DATETIME      | -             |                   | Image expiry (user supplied, 0 = never)
upload\_date    | DATETIME      | -             | NOT NULL          | Image entry creation date
last\_use\_date | DATETIME      | -             |                   | Last time the image was used to spawn a container

Index: UNIQUE ON id AND fingerprint


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


## images\_aliases

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
name            | VARCHAR(255)  | -             | NOT NULL          | Alias name
image\_id       | INTEGER       | -             | NOT NULL          | images.id FK
description     | VARCHAR(255)  | -             |                   | Description of the alias

Index: UNIQUE ON id AND name

Foreign keys: image\_id REFERENCES images(id)


## profiles

Column          | Type          | Default       | Constraint        | Description
:-----          | :---          | :------       | :---------        | :----------
id              | INTEGER       | SERIAL        | NOT NULL          | SERIAL
name            | VARCHAR(255)  | -             | NOT NULL          | Profile name

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
type            | INTEGER       | 0             | NOT NULL          | Container type (see configuration.md)

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
