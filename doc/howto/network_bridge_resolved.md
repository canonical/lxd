(network-bridge-resolved)=
# How to integrate with systemd-resolved

If the system running LXD uses systemd-resolved to perform DNS
lookups, it's possible to notify resolved of the domain(s) that
LXD is able to resolve.  This requires telling resolved the
specific bridge(s), nameserver address(es), and dns domain(s).

For example, if LXD is using the `lxdbr0` interface, get the
ipv4 address with `lxc network get lxdbr0 ipv4.address` command
(the ipv6 can be used instead or in addition), and the domain
with `lxc network get lxdbr0 dns.domain` (if unset, the domain
is `lxd` as shown in the table above).  Then notify resolved:

```
systemd-resolve --interface lxdbr0 --set-domain '~lxd' --set-dns n.n.n.n
```

Replace `lxdbr0` with the actual bridge name, and `n.n.n.n` with
the actual address of the nameserver (without the subnet netmask).

Also replace `lxd` with the domain name.  Note the `~` before the
domain name is important; it tells resolved to use this
nameserver to look up only this domain; no matter what your
actual domain name is, you should prefix it with `~`.  Also,
since the shell may expand the `~` character, you may need to
include it in quotes.

In newer releases of systemd, the `systemd-resolve` command has been
deprecated, however it is still provided for backwards compatibility
(as of this writing).  The newer method to notify resolved is using
the `resolvectl` command, which would be done in two steps:

```
resolvectl dns lxdbr0 n.n.n.n
resolvectl domain lxdbr0 '~lxd'
```

This resolved configuration will persist as long as the bridge
exists, so you must repeat this command each reboot and after
LXD is restarted (see below on how to automate this).

Also note this only works if the bridge `dns.mode` is not `none`.

Note that depending on the `dns.domain` used, you may need to disable
DNSSEC in resolved to allow for DNS resolution. This can be done through
the `DNSSEC` option in `resolved.conf`.

To automate the `systemd-resolved` DNS configuration when LXD creates the `lxdbr0` interface so that it is applied
on system start you need to create a systemd unit file `/etc/systemd/system/lxd-dns-lxdbr0.service` containing:

```
[Unit]
Description=LXD per-link DNS configuration for lxdbr0
BindsTo=sys-subsystem-net-devices-lxdbr0.device
After=sys-subsystem-net-devices-lxdbr0.device

[Service]
Type=oneshot
ExecStart=/usr/bin/resolvectl dns lxdbr0 n.n.n.n
ExecStart=/usr/bin/resolvectl domain lxdbr0 '~lxd'

[Install]
WantedBy=sys-subsystem-net-devices-lxdbr0.device
```

Be sure to replace `n.n.n.n` in that file with the IP of the `lxdbr0` bridge.

Then enable and start it using:

```
sudo systemctl daemon-reload
sudo systemctl enable --now lxd-dns-lxdbr0
```

If the `lxdbr0` interface already exists (i.e LXD is running), then you can check that the new service has started:

```
sudo systemctl status lxd-dns-lxdbr0.service
● lxd-dns-lxdbr0.service - LXD per-link DNS configuration for lxdbr0
     Loaded: loaded (/etc/systemd/system/lxd-dns-lxdbr0.service; enabled; vendor preset: enabled)
     Active: inactive (dead) since Mon 2021-06-14 17:03:12 BST; 1min 2s ago
    Process: 9433 ExecStart=/usr/bin/resolvectl dns lxdbr0 n.n.n.n (code=exited, status=0/SUCCESS)
    Process: 9434 ExecStart=/usr/bin/resolvectl domain lxdbr0 ~lxd (code=exited, status=0/SUCCESS)
   Main PID: 9434 (code=exited, status=0/SUCCESS)
```

You can then check it has applied the settings using:

```
sudo resolvectl status lxdbr0
Link 6 (lxdbr0)
      Current Scopes: DNS
DefaultRoute setting: no
       LLMNR setting: yes
MulticastDNS setting: no
  DNSOverTLS setting: no
      DNSSEC setting: no
    DNSSEC supported: no
  Current DNS Server: n.n.n.n
         DNS Servers: n.n.n.n
          DNS Domain: ~lxd
```
