(initialize)=
# How to initialize LXD

Before you can create a LXD instance, you must configure and initialize LXD.

## Interactive configuration

Run the following command to start the interactive configuration process:

    lxd init

```{note}
For simple configurations, you can run this command as a normal user.
However, some more advanced operations during the initialization process (for example, joining an existing cluster) require root privileges.
In this case, run the command with `sudo` or as root.
```

The tool asks a series of questions to determine the required configuration.
The questions are dynamically adapted to the answers that you give.
They cover the following areas:

Clustering (see {ref}`exp-clustering` and {ref}`cluster-form`)
: A cluster combines several LXD servers.
  The cluster members share the same distributed database and can be managed uniformly using the LXD client (`lxc`) or the REST API.

  The default answer is `no`, which means clustering is not enabled.
  If you answer `yes`, you can either connect to an existing cluster or create one.

MAAS support (see [`maas.io`](https://maas.io/) and [MAAS - How to manage VM hosts](https://maas.io/docs/install-with-lxd))
: MAAS is an open-source tool that lets you build a data center from bare-metal servers.

  The default answer is `no`, which means MAAS support is not enabled.
  If you answer `yes`, you can connect to an existing MAAS server and specify the `name`, `URL` and `API key`.

Networking (see {ref}`networks` and {ref}`Network devices <devices-nic>`)
: Provides network access for the instances.

  You can let LXD create a new bridge (recommended) or use an existing network bridge or interface.

  You can create additional bridges and assign them to instances later.

Storage pools (see {ref}`exp-storage` and  {ref}`storage-drivers`)
: Instances (and other data) are stored in storage pools.

  For testing purposes, you can create a loop-backed storage pool.
  For production use, however, you should use an empty partition (or full disk) instead of loop-backed storage (because loop-backed pools are slower and their size can't be reduced).

  The recommended backends are `zfs` and `btrfs`.

  You can create additional storage pools later.

Remote access (see {ref}`security_remote_access` and {ref}`authentication`)
: Allows remote access to the server over the network.

  The default answer is `no`, which means remote access is not allowed.
  If you answer `yes`, you can connect to the server over the network.

  You can choose to add client certificates to the server (manually or through tokens, the recommended way) or set a trust password.

Automatic image update (see {ref}`image-handling`)
: You can download images from image servers.
  In this case, images can be updated automatically.

  The default answer is `yes`, which means that LXD will update the downloaded images regularly.

YAML `lxd init` preseed (see {ref}`initialize-preseed`)
: If you answer `yes`, the command displays a summary of your chosen configuration options in the terminal.

### Minimal setup

To create a minimal setup with default options, you can skip the configuration steps by adding the `--minimal` flag to the `lxd init` command:

    lxd init --minimal

```{note}
The minimal setup provides a basic configuration, but the configuration is not optimized for speed or functionality.
Especially the [`dir` storage driver](storage-dir), which is used by default, is slower than other drivers and doesn't provide fast snapshots, fast copy/launch, quotas and optimized backups.

If you want to use an optimized setup, go through the interactive configuration process instead.
```
