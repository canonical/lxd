# Frequently asked questions

## General issues

### How to enable LXD server for remote access?

By default, the LXD server is not accessible from the network as it only listens
on a local Unix socket. You can make LXD available from the network by specifying
additional addresses to listen to. This is done with the `core.https_address`
configuration variable.

To see the current server configuration, run:

```bash
lxc config show
```

To set the address to listen to, first find out what addresses are available and
then use the `config set` command on the server:

```bash
ip addr
lxc config set core.https_address 192.0.2.1
```

Also see {ref}`security_remote_access`.

### When I do a `lxc remote add` over HTTPS, it asks for a password?

By default, LXD has no password for security reasons, so you can't do a remote
add this way. To set a password, enter the following command on the host LXD is
running on:

```bash
lxc config set core.trust_password SECRET
```

This will set the remote password that you can then use to do `lxc remote add`.

You can also access the server without setting a password by copying the client
certificate (`~/.config/lxc/client.crt` or `~/snap/lxd/common/config/client.crt`
for snap users) to the server and adding it with:

```bash
lxc config trust add client.crt
```

See {doc}`authentication` for detailed information.

### Can I bind-mount my home directory in a container?

Yes. This can be done using a disk device:

```bash
lxc config device add container-name home disk source=/home/${USER} path=/home/ubuntu
```

For unprivileged containers, you will also need one of:

- Pass `shift=true` to the `lxc config device add` call. This depends on shiftfs being supported (see `lxc info`)
- `raw.idmap` entry (see [Idmaps for user namespace](userns-idmap.md))
- Recursive POSIX ACLs placed on your home directory

Either of those can be used to allow the user in the container to have working read/write permissions.
When not setting one of those, everything will show up as the overflow UID/GID (65536:65536)
and access to anything that's not world readable will fail.

Privileged containers do not have this issue because all UID/GID in the container are the same as outside.
But that's also the cause of most of the security issues with such privileged containers.

### How can I run Docker inside a LXD container?

To run Docker inside a LXD container, the `security.nesting` property of the container should be set to `true`.

```bash
lxc config set <container> security.nesting true
```

Note that LXD containers cannot load kernel modules, so depending on your
Docker configuration you might need to have the needed extra kernel modules
loaded by the host.

You can do so by setting a comma-separated list of kernel modules that your container needs with:

```bash
lxc config set <container> linux.kernel_modules <modules>
```

We have also received some reports that creating a `/.dockerenv` file in your
container can help Docker ignore some errors it's getting due to running in a
nested environment.

### Where does `lxc` store its configuration?

The `lxc` command stores its configuration under `~/.config/lxc` or in `~/snap/lxd/common/config`
for snap users.

Various configuration files are stored in that directory, among which are:

- `client.crt`: client certificate (generated on demand)
- `client.key`: client key (generated on demand)
- `config.yml`: configuration file (info about `remotes`, `aliases`, etc)
- `servercerts/`: directory with server certificates belonging to `remotes`

## Networking issues

In a larger [Production Environment](performance-tuning), it is common to have
multiple VLANs and have LXD clients attached directly to those VLANs. Be aware that
if you are using `netplan` and `systemd-networkd`, you will encounter some bugs that
could cause catastrophic issues.

### Do not use `systemd-networkd` with `netplan` and bridges based on VLANs

At time of writing (2019-03-05), `netplan` cannot assign a random MAC address to
a bridge attached to a VLAN. It always picks the same MAC address, which causes
layer2 issues when you have more than one machine on the same network segment.
It also has difficulty creating multiple bridges.  Make sure you use
`network-manager` instead. An example configuration is below, with a management
address of 10.61.0.25, and VLAN102 being used for client traffic.

    network:
      version: 2
      renderer: NetworkManager
      ethernets:
        eth0:
          dhcp4: no
          accept-ra: no
          # This is the 'Management Address'
          addresses: [ 10.61.0.25/24 ]
          gateway4: 10.61.0.1
          nameservers:
            addresses: [ 1.1.1.1, 8.8.8.8 ]
        eth1:
          dhcp4: no
          accept-ra: no
          # A bogus IP address is required to ensure the link state is up
          addresses: [ 10.254.254.25/32 ]

      vlans:
        vlan102:
          accept-ra: no
          dhcp4: no
          id: 102
          link: eth1

      bridges:
        br102:
          accept-ra: no
          dhcp4: no
          interfaces: [ "vlan102" ]
          # A bogus IP address is required to ensure the link state is up
          addresses: [ 10.254.102.25/32 ]
          parameters:
            stp: false

#### Things to note

- `eth0` is the Management interface, with the default gateway.
- `vlan102` uses `eth1`.
- `br102` uses `vlan102`, and has a bogus /32 IP address assigned to it.

The other important thing is to set `stp: false`, otherwise the bridge will sit
in `learning` state for up to 10 seconds, which is longer than most DHCP requests
last. As there is no possibility of cross-connecting and causing loops, this is
safe to do.

### Beware of port security

Many switches do not allow MAC address changes, and will either drop traffic
with an incorrect MAC or disable the port totally. If you can ping a LXD instance
from the host, but are not able to ping it from a different host, this could be
the cause.  The way to diagnose this is to run a `tcpdump` on the uplink (in this case,
`eth1`), and you will see either "ARP Who has `xx.xx.xx.xx` tell `yy.yy.yy.yy`", with you
sending responses but them not getting acknowledged, or ICMP packets going in and
out successfully, but never being received by the other host.

### Do not run privileged containers unless necessary

A privileged container can do things that affect the entire host - for example, it
can use things in `/sys` to reset the network card, which will reset it for **the entire
host**, causing network blips. Almost everything can be run in an unprivileged container,
or - in cases of things that require unusual privileges, like wanting to mount NFS
file systems inside the container - you might need to use bind mounts.
