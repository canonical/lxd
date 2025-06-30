(instances-access-files)=
# How to access files in an instance

You can manage files inside an instance using the LXD client or the API without needing to access the instance through the network.
Files can be individually edited or deleted, pushed from or pulled to the local machine.
Alternatively, if you're using the LXD client, you can mount the instance's file system onto the local machine.

```{note}
The UI does not currently support accessing files in an instance.
```

For containers, these file operations always work and are handled directly by LXD.
For virtual machines, the `lxd-agent` process must be running inside of the virtual machine for them to work.

## Edit instance files

`````{tabs}
````{group-tab} CLI
To edit an instance file from your local machine, enter the following command:

    lxc file edit <instance_name>/<path_to_file>

For example, to edit the `/etc/hosts` file in the instance, enter the following command:

    lxc file edit my-instance/etc/hosts

```{note}
The file must already exist on the instance.
You cannot use the `edit` command to create a file on the instance.
```
````
````{group-tab} API
There is no API endpoint that lets you edit files directly on an instance.
Instead, you need to {ref}`pull the content of the file from the instance <instances-access-files-pull>`, edit it, and then {ref}`push the modified content back to the instance <instances-access-files-push>`.
````
`````

## Delete files from the instance

````{tabs}
```{group-tab} CLI
To delete a file from your instance, enter the following command:

    lxc file delete <instance_name>/<path_to_file>
```
```{group-tab} API
Send the following DELETE request to delete a file from your instance:

    lxc query --request DELETE /1.0/instances/<instance_name>/files?path=<path_to_file>

See [`DELETE /1.0/instances/{name}/files`](swagger:/instances/instance_files_delete) for more information.
```
````

(instances-access-files-pull)=
## Pull files from the instance to the local machine

````{tabs}
```{group-tab} CLI
To pull a file from your instance to your local machine, enter the following command:

    lxc file pull <instance_name>/<path_to_file> <local_file_path>

For example, to pull the `/etc/hosts` file to the current directory, enter the following command:

    lxc file pull my-instance/etc/hosts .

Instead of pulling the instance file into a file on the local system, you can also pull it to stdout and pipe it to stdin of another command.
This can be useful, for example, to check a log file:

    lxc file pull my-instance/var/log/syslog - | less

To pull a directory with all contents, enter the following command:

    lxc file pull -r <instance_name>/<path_to_directory> <local_location>
```
```{group-tab} API
Send the following request to pull the contents of a file from your instance to your local machine:

    lxc query --request GET /1.0/instances/<instance_name>/files?path=<path_to_file>

You can then write the contents to a local file, or pipe them to stdin of another command.

For example, to pull the contents of the `/etc/hosts` file and write them to a `my-instance-hosts` file in the current directory, enter the following command:

    lxc query --request GET /1.0/instances/my-instance/files?path=/etc/hosts > my-instance-hosts

To examine a log file, enter the following command:

    lxc query --request GET /1.0/instances/<instance_name>/files?path=<file_path> | less

To pull the contents of a directory, send the following request:

    lxc query --request GET /1.0/instances/<instance_name>/files?path=<path_to_directory>

This request returns a list of files in the directory, and you can then pull the contents of each file.

See [`GET /1.0/instances/{name}/files`](swagger:/instances/instance_files_get) for more information.
```
````

(instances-access-files-push)=
## Push files from the local machine to the instance

````{tabs}
```{group-tab} CLI
To push a file from your local machine to your instance, enter the following command:

    lxc file push <local_file_path> <instance_name>/<path_to_file>

You can specify the file permissions by adding the `--gid`, `--uid`, and `--mode` flags.

To push a directory with all contents, enter the following command:

    lxc file push -r <local_location> <instance_name>/<path_to_directory>
```
```{group-tab} API
Send the following request to write content to a file on your instance:

    lxc query --request POST /1.0/instances/<instance_name>/files?path=<path_to_file> --data <content>

See [`POST /1.0/instances/{name}/files`](swagger:/instances/instance_files_post) for more information.

To push content directly from a file, you must use a tool that can send raw data from a file, which [`lxc query`](lxc_query.md) does not support.
For example, with curl:

    curl -X POST -H "Content-Type: application/octet-stream" --data-binary @<local_file_path> \
    --unix-socket /var/snap/lxd/common/lxd/unix.socket \
    lxd/1.0/instances/<instance_name>/files?path=<path_to_file>
```
````

## Mount a file system from the instance

`````{tabs}
````{group-tab} CLI
You can mount an instance file system into a local path on your client.

To do so, make sure that you have `sshfs` installed.
Then run the following command (note that if you're using the snap, the command requires root permissions):

    lxc file mount <instance_name>/<path_to_directory> <local_location>

You can then access the files from your local machine.

### Set up an SSH SFTP listener

Alternatively, you can set up an SSH SFTP listener.
This method allows you to connect with any SFTP client and with a dedicated user name.
Also, if you're using the snap, it does not require root permission.

To do so, first set up the listener by entering the following command:

    lxc file mount <instance_name> [--listen <address>:<port>]

For example, to set up the listener on a random port on the local machine (for example, `127.0.0.1:45467`):

    lxc file mount my-instance

If you want to access your instance files from outside your local network, you can pass a specific address and port:

    lxc file mount my-instance --listen 192.0.2.50:2222

```{caution}
Be careful when doing this, because it exposes your instance remotely.
```

To set up the listener on a specific address and a random port:

    lxc file mount my-instance --listen 192.0.2.50:0

The command prints out the assigned port and a user name and password for the connection.

```{tip}
You can specify a user name by passing the `--auth-user` flag.
```

Use this information to access the file system.
For example, if you want to use `sshfs` to connect, enter the following command:

    sshfs <user_name>@<address>:<path_to_directory> <local_location> -p <port>

For example:

    sshfs xFn8ai8c@127.0.0.1:/home my-instance-files -p 35147

You can then access the file system of your instance at the specified location on the local machine.
````
````{group-tab} API
Mounting a file system is not directly supported through the API, but requires additional processing logic on the client side.
````
`````
