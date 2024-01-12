(run-commands)=
# How to run commands in an instance

LXD allows to run commands inside an instance using the LXD client, without needing to access the instance through the network.

For containers, this always works and is handled directly by LXD.
For virtual machines, the `lxd-agent` process must be running inside of the virtual machine for this to work.

To run commands inside your instance, use the [`lxc exec`](lxc_exec.md) command.
By running a shell command (for example, `/bin/bash`), you can get shell access to your instance.

## Run commands inside your instance

````{tabs}
```{group-tab} CLI
To run a single command from the terminal of the host machine, use the [`lxc exec`](lxc_exec.md) command:

    lxc exec <instance_name> -- <command>

For example, enter the following command to update the package list on your container:

    lxc exec my-instance -- apt-get update
```
```{group-tab} API
Send a POST request to the instance's `exec` endpoint to run a single command from the terminal of the host machine:

    lxc query --request POST /1.0/instances/<instance_name>/exec --data '{
      "command": [ "<command>" ]
    }'

For example, enter the following command to update the package list on your container:

    lxc query --request POST /1.0/instances/my-instance/exec --data '{
      "command": [ "apt-get", "update" ]
    }'

See [`POST /1.0/instances/{name}/exec`](swagger:/instances/instance_exec_post) for more information.
```
````

### Execution mode

LXD can execute commands either interactively or non-interactively.

````{tabs}
```{group-tab} CLI
In interactive mode, a pseudo-terminal device (PTS) is used to handle input (stdin) and output (stdout, stderr).
This mode is automatically selected by the CLI if connected to a terminal emulator (and not run from a script).
To force interactive mode, add either `--force-interactive` or `--mode interactive` to the command.

In non-interactive mode, pipes are allocated instead (one for each of stdin, stdout and stderr).
This method allows running a command and properly getting separate stdin, stdout and stderr as required by many scripts.
To force non-interactive mode, add either `--force-noninteractive` or `--mode non-interactive` to the command.
```
```{group-tab} API
In both modes, the operation creates a control socket that can be used for out-of-band communication with LXD.
You can send signals and window sizing information through this socket.

Interactive mode
: In interactive mode, the operation creates an additional single bi-directional WebSocket.
  To force interactive mode, add `"interactive": true` and `"wait-for-websocket": true` to the request data.
  For example:

      lxc query --request POST /1.0/instances/my-instance/exec --data '{
        "command": [ "/bin/bash" ],
        "interactive": true,
        "wait-for-websocket": true
      }'

Non-interactive mode
: In non-interactive mode, the operation creates three additional WebSockets: one each for stdin, stdout, and stderr.
  To force non-interactive mode, add `"interactive": false` to the request data.

  When running a command in non-interactive mode, you can instruct LXD to record the output of the command.
  To do so, add `"record-output": true` to the request data.
  You can then send a request to the `exec-output` endpoint to retrieve the list of files that contain command output:

      lxc query --request GET /1.0/instances/<instance_name>/logs/exec-output

  To display the output of one of the files, send a request to one of the files:

      lxc query --request GET /1.0/instances/<instance_name>/logs/exec-output/<record-output-file>

  When you don't need the command output anymore, you can delete it:

      lxc query --request DELETE /1.0/instances/<instance_name>/logs/exec-output/<record-output-file>

  See [`GET /1.0/instances/{name}/logs/exec-output`](swagger:/instances/instance_exec-outputs_get), [`GET /1.0/instances/{name}/logs/exec-output/{filename}`](swagger:/instances/instance_exec-output_get), and [`DELETE /1.0/instances/{name}/logs/exec-output/{filename}`](swagger:/instances/instance_exec-output_delete) for more information.
```
````

### User, groups and working directory

LXD has a policy not to read data from within the instances or trust anything that can be found in the instance.
Therefore, LXD does not parse files like `/etc/passwd`, `/etc/group` or `/etc/nsswitch.conf` to handle user and group resolution.

As a result, LXD doesn't know the home directory for the user or the supplementary groups the user is in.

By default, LXD runs commands as `root` (UID 0) with the default group (GID 0) and the working directory set to `/root`.
You can override the user, group and working directory by specifying absolute values.

````{tabs}
```{group-tab} CLI
You can override the default settings by adding the following flags to the [`lxc exec`](lxc_exec.md) command:

- `--user` - the user ID for running the command
- `--group` - the group ID for running the command
- `--cwd` - the directory in which the command should run

```
```{group-tab} API
You can override the default settings by adding the following fields to the request data:

- `"user": <user_ID>` - the user ID for running the command
- `"group": <group_ID>` - the group ID for running the command
- `"cwd": "<directory>"` - the directory in which the command should run

```
````

### Environment

You can pass environment variables to an exec session in the following two ways:

Set environment variables as instance options
: ````{tabs}

  ```{group-tab} CLI
  To set the `<ENVVAR>` environment variable to `<value>` in the instance, set the `environment.<ENVVAR>` instance option (see {config:option}`instance-miscellaneous:environment.*`):

      lxc config set <instance_name> environment.<ENVVAR>=<value>
  ```

  ```{group-tab} API
  To set the `<ENVVAR>` environment variable to `<value>` in the instance, set the `environment.<ENVVAR>` instance option (see {config:option}`instance-miscellaneous:environment.*`):

      lxc query --request PATCH /1.0/instances/<instance_name> --data '{
        "config": {
          "environment.<ENVVAR>": "<value>"
        }
      }'
  ```

  ````

Pass environment variables to the exec command
: ````{tabs}

  ```{group-tab} CLI
  To pass an environment variable to the exec command, use the `--env` flag.
  For example:

      lxc exec <instance_name> --env <ENVVAR>=<value> -- <command>
  ```

  ```{group-tab} API
  To pass an environment variable to the exec command, add an `environment` field to the request data.
  For example:

      lxc query --request POST /1.0/instances/<instance_name>/exec --data '{
        "command": [ "<command>" ],
        "environment": {
          "<ENVVAR>": "<value>"
        }
      }'
  ```

  ````

In addition, LXD sets the following default values (unless they are passed in one of the ways described above):

```{list-table}
   :header-rows: 1

* - Variable name
  - Condition
  - Value
* - `PATH`
  - \-
  - Concatenation of:
    - `/usr/local/sbin`
    - `/usr/local/bin`
    - `/usr/sbin`
    - `/usr/bin`
    - `/sbin`
    - `/bin`
    - `/snap` (if applicable)
    - `/etc/NIXOS` (if applicable)
* - `LANG`
  - \-
  - `C.UTF-8`
* - `HOME`
  - running as root (UID 0)
  - `/root`
* - `USER`
  - running as root (UID 0)
  - `root`
```

## Get shell access to your instance

If you want to run commands directly in your instance, run a shell command inside it.
For example, enter the following command (assuming that the `/bin/bash` command exists in your instance):

````{tabs}
```{group-tab} CLI
    lxc exec <instance_name> -- /bin/bash
```
```{group-tab} API
    lxc query --request POST /1.0/instances/<instance_name>/exec --data '{
      "command": [ "/bin/bash" ]
    }'
```
````

By default, you are logged in as the `root` user.
If you want to log in as a different user, enter the following command:

````{tabs}
```{group-tab} CLI
    lxc exec <instance_name> -- su --login <user_name>

To exit the instance shell, enter `exit` or press {kbd}`Ctrl`+{kbd}`d`.
```
```{group-tab} API
    lxc query --request POST /1.0/instances/<instance_name>/exec --data '{
      "command": [ "su", "--login", "<user_name>" ]
    }'
```
````

```{note}
Depending on the operating system that you run in your instance, you might need to create a user first.
```
