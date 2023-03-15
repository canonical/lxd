(migrate-from-lxc)=
# How to migrate containers from LXC to LXD

```{youtube} https://www.youtube.com/watch?v=F9GALjHtnUU
```

LXD provides a tool (`lxc-to-lxd`) that you can use to import LXC containers into your LXD server.
The LXC containers must exist on the same machine as the LXD server.

The tool analyzes the LXC containers and migrates both their data and their configuration into new LXD containers.

```{note}
Alternatively, you can use the `lxd-migrate` tool within a LXC container to migrate it to LXD (see {ref}`import-machines-to-instances`).
However, this tool does not migrate any of the LXC container configuration.
```

## Get the tool

If you're using the snap, the `lxc-to-lxd` is automatically installed.
It is available as `lxd.lxc-to-lxd`.

Otherwise, make sure that you have `go` (version 1.18 or later) installed and get the tool with the following command:

    go install github.com/lxc/lxd/lxc-to-lxd@latest

## Prepare your LXC containers

You can migrate one container at a time or all of your LXC containers at the same time.

```{note}
Migrated containers use the same name as the original containers.
You cannot migrate containers with a name that already exists as an instance name in LXD.

Therefore, rename any LXC containers that might cause name conflicts before you start the migration process.
```

Before you start the migration process, stop the LXC containers that you want to migrate.

## Start the migration process

Run `sudo lxd.lxc-to-lxd [flags]` to migrate the containers.
(This command assumes that you are using the snap; otherwise, replace `lxd.lxc-to-lxd` with `lxc-to-lxd`, also in the following examples.)

For example, to migrate all containers:

    sudo lxd.lxc-to-lxd --all

To migrate only the `lxc1` container:

    sudo lxd.lxc-to-lxd --containers lxc1

To migrate two containers (`lxc1` and `lxc2`) and use the `my-storage` storage pool in LXD:

    sudo lxd.lxc-to-lxd --containers lxc1,lxc2 --storage my-storage

To test the migration of all containers without actually running it:

    sudo lxd.lxc-to-lxd --all --dry-run

To migrate all containers but limit the `rsync` bandwidth to 5000 KB/s:

    sudo lxd.lxc-to-lxd --all --rsync-args --bwlimit=5000

Run `sudo lxd.lxc-to-lxd --help` to check all available flags.

```{note}
If you get an error that the `linux64` architecture isn't supported, either update the tool to the latest version or change the architecture in the LXC container configuration from `linux64` to either `amd64` or `x86_64`.
```

## Check the configuration

The tool analyzes the LXC configuration and the configuration of the container (or containers) and migrates as much of the configuration as possible.
You will see output similar to the following:

```{terminal}
:input: sudo lxd.lxc-to-lxd --containers lxc1

Parsing LXC configuration
Checking for unsupported LXC configuration keys
Checking for existing containers
Checking whether container has already been migrated
Validating whether incomplete AppArmor support is enabled
Validating whether mounting a minimal /dev is enabled
Validating container rootfs
Processing network configuration
Processing storage configuration
Processing environment configuration
Processing container boot configuration
Processing container apparmor configuration
Processing container seccomp configuration
Processing container SELinux configuration
Processing container capabilities configuration
Processing container architecture configuration
Creating container
Transferring container: lxc1: ...
Container 'lxc1' successfully created
```

After the migration process is complete, you can check and, if necessary, update the configuration in LXD before you start the migrated LXD container.
