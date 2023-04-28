# How to configure the LXD server

- Check configuration
- Update configuration options
- Edit full configuration

You can configure a server option with the following command:

    lxc config set <key> <value>

If the LXD server is part of a cluster, some of the options apply to the cluster, while others apply only to the local server, thus the cluster member.
Options marked with a `global` scope in the following tables are immediately applied to all cluster members.
Options with a `local` scope must be set on a per-member basis.
To do so, add the `--target` flag to the `lxc config set` command.
