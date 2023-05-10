---
discourse: 24
---

# About `lxd` and `lxc`

LXD is frequently confused with LXC, and the fact that LXD provides both a `lxd` command and a `lxc` command doesn't make things easier.

## LXD vs. LXC

LXD and LXC are two distinct implementations of Linux containers.

[LXC](https://linuxcontainers.org/lxc/introduction/) is a low-level user space interface for the Linux kernel containment features.
It consists of tools (`lxc-*` commands), templates, and library and language bindings.

[LXD](https://linuxcontainers.org/lxd/introduction/) is a more intuitive and user-friendly tool aimed at making it easy to work with Linux containers.
It is an alternative to LXC's tools and distribution template system, with the added features that come from being controllable over the network.
Under the hood, LXD uses LXC to create and manage the containers.

LXD provides a superset of the features that LXC supports, and it is easier to use.
Therefore, if you are unsure which of the tools to use, you should go for LXD.
LXC should be seen as an alternative for experienced users that want to run Linux containers on distributions that don't support LXD.

(lxd-daemon)=
## LXD daemon

The central part of LXD is its daemon.
It runs persistently in the background, manages the instances, and handles all requests.
The daemon provides a REST API that you can access directly or through a client (for example, the default command-line client that comes with LXD).

See {ref}`daemon-behavior` for more information about the LXD daemon.

## `lxd` vs. `lxc`

To control LXD, you typically use two different commands: `lxd` and `lxc`.

LXD daemon
: The `lxd` command controls the LXD daemon.
  Since the daemon is typically started automatically, you hardly ever need to use the `lxd` command.
  An exception is the `lxd init` subcommand that you run to {ref}`initialize LXD <initialize>`.

  There are also some subcommands for debugging and administrating the daemon, but they are intended for advanced users only.
  See `lxd --help` for an overview of all available subcommands.

LXD client
: The `lxc` command is a command-line client for LXD, which you can use to interact with the LXD daemon.
  You use the `lxc` command to manage your instances, the server settings, and overall the entities you create in LXD.
  See `lxc --help` for an overview of all available subcommands.

  The `lxc` tool is not the only client you can use to interact with the LXD daemon.
  You can also use the API, the UI, or a custom LXD client.
