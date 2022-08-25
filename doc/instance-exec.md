# Instance command execution

LXD makes it easy to run a command inside a given instance.
For containers, this always works and is handled directly by LXD.
For virtual machines, this relies on the `lxd-agent` process running inside of the virtual machine.

At the CLI level, this is achieved through the `lxc exec` command which
supports specifying not only the command to executed but also the
execution mode, user, group and working directory.

At the API level, this is done through `/1.0/instances/NAME/exec`.

## Execution mode

LXD can execute commands either interactively or non-interactively.

In interactive mode, a pseudo-terminal device (PTS) will be used to handle input (stdin) and output (stdout, stderr).
This is automatically selected by the CLI if connected to a terminal emulator (not run from a script).

In non-interactive mode, pipes are allocated instead, one for each of stdin, stdout and stderr.
This allows running a command and properly getting separate stdin, stdout and stderr as required by many scripts.

## User, groups and working directory

LXD has a policy not to read data from within the instances or trusting anything that can be found in it.
This means that LXD will not be parsing things like `/etc/passwd`, `/etc/group` or `/etc/nsswitch.conf`
to handle user and group resolution.

As a result, LXD also doesn't know where the home directory for the user
may be or what supplementary groups the user may be in.

By default, LXD will run the command as root (UID 0) with the default group (GID 0)
and the working directory set to /root.

The user, group and working directory can all be overridden but absolute values (UID, GID, path)
have to be provided as LXD will not do any resolution for you.

## Environment

The environment variables set during an exec session come from a few sources:

- `environment.KEY=VALUE` directly set on the instance
- Environment variables directly passed during the exec session
- Default variables set by LXD

For that last category, LXD will set the `PATH` to `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`
and extend it with `/snap` and `/etc/NIXOS` if applicable.
Additionally `LANG` will be set to `C.UTF-8`.

When running as root (UID 0), the following variables will also be set:

- `HOME` to `/root`
- `USER` to `root`

When running as another user, it is the responsibility of the user to specify the correct values.

Those defaults only get set if they're not in the instance configuration or directly overridden for the exec session.
