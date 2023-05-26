# Frequently asked questions

The following sections give answers to frequently asked questions.
They explain how to resolve common issues and point you to more detailed information.

## Why do my instances not have network access?

Most likely, your firewall blocks network access for your instances.
See {ref}`network-bridge-firewall` for more information about the problem and how to fix it.

Another frequent reason for connectivity issues is running LXD and Docker on the same host.
See {ref}`network-lxd-docker` for instructions on how to fix such issues.

## How to enable the LXD server for remote access?

By default, the LXD server is not accessible from the network, because it only listens on a local Unix socket.

You can enable it for remote access by following the instructions in {ref}`server-expose`.

## When I do a `lxc remote add`, it asks for a password or token?

To be able to access the remote API, clients must authenticate with the LXD server.
Depending on how the remote server is configured, you must provide either a trust token issued by the server or specify a trust password (if [`core.trust_password`](server-options-core) is set).

See {ref}`server-authenticate` for instructions on how to authenticate using a trust token (the recommended way), and  {doc}`authentication` for information about other authentication methods.

## Why should I not run privileged containers?

A privileged container can do things that affect the entire host - for example, it can use things in `/sys` to reset the network card, which will reset it for the entire host, causing network blips.
See {ref}`container-security` for more information.

Almost everything can be run in an unprivileged container, or - in cases of things that require unusual privileges, like wanting to mount NFS file systems inside the container - you might need to use bind mounts.

## Can I bind-mount my home directory in a container?

Yes, you can do this by using a {ref}`disk device <devices-disk>`:

    lxc config device add container-name home disk source=/home/${USER} path=/home/ubuntu

For unprivileged containers, you need to make sure that the user in the container has working read/write permissions.
Otherwise, all files will show up as the overflow UID/GID (`65536:65536`) and access to anything that's not world-readable will fail.
Use either of the following methods to grant the required permissions:

- Pass `shift=true` to the `lxc config device add` call. This depends on the kernel and file system supporting either idmapped mounts or shiftfs (see `lxc info`).
- Add a `raw.idmap` entry (see [Idmaps for user namespace](userns-idmap.md)).
- Place recursive POSIX ACLs on your home directory.

Privileged containers do not have this issue because all UID/GID in the container are the same as outside.
But that's also the cause of most of the security issues with such privileged containers.

## How can I run Docker inside a LXD container?

```{youtube} https://www.youtube.com/watch?v=_fCSSEyiGro
```

To run Docker inside a LXD container, set the [`security.nesting`](instance-options-security) property of the container to `true`:

    lxc config set <container> security.nesting true

Note that LXD containers cannot load kernel modules, so depending on your Docker configuration, you might need to have extra kernel modules loaded by the host.
You can do so by setting a comma-separated list of kernel modules that your container needs:

    lxc config set <container_name> linux.kernel_modules <modules>

In addition, creating a `/.dockerenv` file in your container can help Docker ignore some errors it's getting due to running in a nested environment.

## Where does the LXD client (`lxc`) store its configuration?

The `lxc` command stores its configuration under `~/.config/lxc`, or in `~/snap/lxd/common/config` for snap users.

Various configuration files are stored in that directory, for example:

- `client.crt`: client certificate (generated on demand)
- `client.key`: client key (generated on demand)
- `config.yml`: configuration file (info about `remotes`, `aliases`, etc.)
- `servercerts/`: directory with server certificates belonging to `remotes`

## Why can I not ping my LXD instance from another host?

Many switches do not allow MAC address changes, and will either drop traffic with an incorrect MAC or disable the port totally.
If you can ping a LXD instance from the host, but are not able to ping it from a different host, this could be the cause.

The way to diagnose this problem is to run a `tcpdump` on the uplink and you will see either ``ARP Who has `xx.xx.xx.xx` tell `yy.yy.yy.yy` ``, with you sending responses but them not getting acknowledged, or ICMP packets going in and out successfully, but never being received by the other host.
