(move-instances)=
# How to move existing LXD instances between servers

To move an instance from one LXD server to another, use the `lxc move` command:

    lxc move [<source_remote>:]<source_instance_name> <target_remote>:[<target_instance_name>]

```{note}
When moving a container, you must stop it first.
See {ref}`live-migration` for more information.
```

You don't need to specify the source remote if it is your default remote, and you can leave out the target instance name if you want to use the same instance name.
If you want to move the instance to a specific cluster member, specify it with the `--target` flag.
In this case, do not specify the source and target remote.

You can add the `--mode` flag to choose a transfer mode, depending on your network setup:

`pull` (default)
: Instruct the target server to connect to the source server and pull the respective instance.

`push`
: Instruct the source server to connect to the target server and push the instance.

`relay`
: Instruct the client to connect to both the source and the target server and transfer the data through the client.

If you need to adapt the configuration for the instance to run on the target server, you can either specify the new configuration directly (using `--config`, `--device`, `--storage` or `--target-project`) or through profiles (using `--no-profiles` or `--profile`). See `lxc move --help` for all available flags.

(live-migration)=
## Live migration

Virtual machines can be moved to another server while they are running, thus without any downtime.

For containers, there is limited support for live migration using [{abbr}`CRIU (Checkpoint/Restore in Userspace)`](https://criu.org/).
However, because of extensive kernel dependencies, only very basic containers (non-`systemd` containers without a network device) can be migrated reliably.
In most real-world scenarios, you should stop the container, move it over and then start it again.

If you want to use live migration for containers, you must enable CRIU on both the source and the target server.
If you are using the snap, use the following commands to enable CRIU:

    snap set lxd criu.enable=true
    systemctl reload snap.lxd.daemon

Otherwise, make sure you have CRIU installed on both systems.
