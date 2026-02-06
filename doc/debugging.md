# How to debug LXD

For information on debugging instance issues, see {ref}`instances-troubleshoot`.

## Debugging `lxc` and `lxd`

Here are different ways to help troubleshooting `lxc` and `lxd` code.

### `lxc --debug`

Adding `--debug` flag to any client command will give extra information
about internals. If there is no useful info, it can be added with the
logging call:

    logger.Debugf("Hello: %s", "Debug")

### `lxc monitor`

This command will monitor messages as they appear on remote server.

## REST API through local socket

On server side the most easy way is to communicate with LXD through
local socket. This command accesses `GET /1.0` and formats JSON into
human readable form using [jq](https://stedolan.github.io/jq/tutorial/)
utility:

```bash
curl --unix-socket /var/lib/lxd/unix.socket lxd/1.0 | jq .
```

or for snap users:

```bash
curl --unix-socket /var/snap/lxd/common/lxd/unix.socket lxd/1.0 | jq .
```

See the [RESTful API](rest-api.md) for available API.

## REST API through HTTPS

{ref}`HTTPS connection to LXD <security>` requires valid
client certificate that is generated on first [`lxc remote add`](lxc_remote_add.md). This
certificate should be passed to connection tools for authentication
and encryption.

If desired, `openssl` can be used to examine the certificate (`~/.config/lxc/client.crt`
or `~/snap/lxd/common/config/client.crt` for snap users):

```bash
openssl x509 -text -noout -in client.crt
```

Among the lines you should see:

    Certificate purposes:
    SSL client : Yes

### With command line tools

```bash
wget --no-check-certificate --certificate=$HOME/.config/lxc/client.crt --private-key=$HOME/.config/lxc/client.key -qO - https://127.0.0.1:8443/1.0

# or for snap users
wget --no-check-certificate --certificate=$HOME/snap/lxd/common/config/client.crt --private-key=$HOME/snap/lxd/common/config/client.key -qO - https://127.0.0.1:8443/1.0
```

### With browser

Some browser plugins provide convenient interface to create, modify
and replay web requests. To authenticate against LXD server, convert
`lxc` client certificate into importable format and import it into
browser.

For example this produces `client.pfx` in Windows-compatible format:

```bash
openssl pkcs12 -clcerts -inkey client.key -in client.crt -export -out client.pfx
```

After that, opening [`https://127.0.0.1:8443/1.0`](https://127.0.0.1:8443/1.0) should work as expected.

## Debug LXD using `pprof`
LXD provides a Go [`pprof`](https://pkg.go.dev/net/http/pprof) server when the {config:option}`server-core:core.debug_address` is set.

The debug server should not be exposed to an externally accessible address for production use cases. Use the following command to enable the server on the loopback interface:

    lxc config set core.debug_address=localhost:8080

If the LXD server is running on your workstation, you can view a summary of available information by navigating to [`http://localhost:8080/debug/pprof/`](http://localhost:8080/debug/pprof/).

## Debug the LXD database

The files of the global {ref}`database <database>` are stored under the `./database/global`
sub-directory of your LXD data directory (e.g. `/var/lib/lxd/database/global` or
`/var/snap/lxd/common/lxd/database/global` for snap users).

Since each member of the cluster also needs to keep some data which is specific
to that member, LXD also uses a plain SQLite database (the "local" database),
which you can find in `./database/local.db`.

Backups of the global database directory and of the local database file are made
before upgrades, and are tagged with the `.bak` suffix. You can use those if
you need to revert the state as it was before the upgrade.

### Dumping the database content or schema

If you want to get a SQL text dump of the content or the schema of the databases,
use the `lxd sql <local|global> [.dump|.schema]` command, which produces the
equivalent output of the `.dump` or `.schema` directives of the `sqlite3`
command line tool.

### Running custom queries from the console

If you need to perform SQL queries (e.g. `SELECT`, `INSERT`, `UPDATE`)
against the local or global database, you can use the `lxd sql` command (run
`lxd sql --help` for details).

You should only need to do that in order to recover from broken updates or bugs.
Please consult the LXD team first (creating a [GitHub
issue](https://github.com/canonical/lxd/issues/new) or
[forum](https://discourse.ubuntu.com/c/project/lxd/126) post).

### Running custom queries at LXD daemon startup

In case the LXD daemon fails to start after an upgrade because of SQL data
migration bugs or similar problems, it's possible to recover the situation by
creating `.sql` files containing queries that repair the broken update.

To perform repairs against the local database, write a
`./database/patch.local.sql` file containing the relevant queries, and
similarly a `./database/patch.global.sql` for global database repairs.

Those files will be loaded very early in the daemon startup sequence and deleted
if the queries were successful (if they fail, no state will change as they are
run in a SQL transaction).

As above, please consult the LXD team first.

### Syncing the cluster database to disk

If you want to flush the content of the cluster database to disk, use the `lxd
sql global .sync` command, that will write a plain SQLite database file into
`./database/global/db.bin`, which you can then inspect with the `sqlite3`
command line tool.

## Inspect a core dump file

In our continuous integration tests, we have configured the `core_pattern` as follows:

    echo '|/bin/sh -c $@ -- eval exec gzip --fast > /var/crash/core-%e.%p.gz' | sudo tee /proc/sys/kernel/core_pattern

Additionally, we have set the `GOTRACEBACK` environment variable to `crash`.
Together, these ensure that when LXD crashes a core dump is compressed with `gzip` and placed in `/var/crash`.

To inspect a core dump file, you will need the LXD binary that was running at the time of the crash.
The binary must include symbols; you can check this with the `file` utility.
You will also need any C libraries that are used by LXD which must also include symbols.

You can inspect a core dump using [Delve](https://github.com/go-delve/delve) (see the [Go Wiki](https://go.dev/wiki/CoreDumpDebugging) for more information), but this does not support any dynamically linked C libraries.
Instead, you can use [GDB](https://sourceware.org/gdb/) which can inspect linked libraries and allows sourcing a file to load Golang support.

To do this, run:

    gdb <LXD binary> <coredump file>

Then in the GDB REPL, run:

    (gdb) source <GOROOT>/src/runtime/runtime-gdb.py

Substituting in the actual path to your `$GOROOT`.
This will add Golang runtime support.

Finally, set the search path for C libraries using:

    (gdb) set solib-search-path <path to C libraries>

You can now use the GDB REPL to inspect the core dump.
Some useful commands are:

- `backtrace` (print stack trace).
- `info goroutines` (show goroutines).
- `info threads` (show threads).
- `thread <thread_number>` (change thread).
