---
discourse: 10034,9953
---

(network-bridge-firewall)=
# How to configure your firewall

Linux firewalls are based on `netfilter`.
LXD uses the same subsystem, which can lead to connectivity issues.

If you run a firewall on your system, you might need to configure it to allow network traffic between the managed LXD bridge and the host.
Otherwise, some network functionality (DHCP, DNS and external network access) might not work as expected.

You might also see conflicts between the rules defined by your firewall (or another application) and the firewall rules that LXD adds.
For example, your firewall might erase LXD rules if it is started after the LXD daemon, which might interrupt network connectivity to the instance.

## `xtables` vs. `nftables`

There are different userspace commands to add rules to `netfilter`: `xtables` (`iptables` for IPv4 and `ip6tables` for IPv6) and `nftables`.

`xtables` provides an ordered list of rules, which might cause issues if multiple systems add and remove entries from the list.
`nftables` adds the ability to separate rules into namespaces, which helps to separate rules from different applications.
However, if a packet is blocked in one namespace, it is not possible for another namespace to allow it.
Therefore, rules in one namespace can still affect rules in another namespace, and firewall applications can still impact LXD network functionality.

If your system supports and uses `nftables`, LXD detects this and switches to `nftables` mode.
In this mode, LXD adds its rules into the `nftables`, using its own `nftables` namespace.

## Use LXD's firewall

By default, managed LXD bridges add firewall rules to ensure full functionality.
If you do not run another firewall on your system, you can let LXD manage its firewall rules.

To enable or disable this behavior, use the `ipv4.firewall` or `ipv6.firewall` {ref}`configuration options <network-bridge-options>`.

## Use another firewall

Firewall rules added by other applications might interfere with the firewall rules that LXD adds.
Therefore, if you use another firewall, you should disable LXD's firewall rules.
You must also configure your firewall to allow network traffic between the instances and the LXD bridge, so that the LXD instances can access the DHCP and DNS server that LXD runs on the host.

See the following sections for instructions on how to disable LXD's firewall rules and how to properly configure `firewalld` and UFW, respectively.

### Disable LXD's firewall rules

Run the following commands to prevent LXD from setting firewall rules for a specific network bridge (for example, `lxdbr0`):

    lxc network set <network_bridge> ipv6.firewall false
    lxc network set <network_bridge> ipv4.firewall false

### `firewalld`: Add the bridge to the trusted zone

To allow traffic to and from the LXD bridge in `firewalld`, add the bridge interface to the `trusted` zone.
To do this permanently (so that it persists after a reboot), run the following commands:

    sudo firewall-cmd --zone=trusted --change-interface=<network_bridge> --permanent
    sudo firewall-cmd --reload

For example:

    sudo firewall-cmd --zone=trusted --change-interface=lxdbr0 --permanent
    sudo firewall-cmd --reload

<!-- Include start warning -->

```{warning}
The commands given above show a simple example configuration.
Depending on your use case, you might need more advanced rules and the example configuration might inadvertently introduce a security risk.
```

<!-- Include end warning -->

### UFW: Add rules for the bridge

If UFW has a rule to drop all unrecognized traffic, it blocks the traffic to and from the LXD bridge.
In this case, you must add rules to allow traffic to and from the bridge, as well as allowing traffic forwarded to it.

To do so, run the following commands:

    sudo ufw allow in on <network_bridge>
    sudo ufw route allow in on <network_bridge>
    sudo ufw route allow out on <network_bridge>

For example:

    sudo ufw allow in on lxdbr0
    sudo ufw route allow in on lxdbr0
    sudo ufw route allow out on lxdbr0

% Repeat warning from above
```{include} network_bridge_firewalld.md
    :start-after: <!-- Include start warning -->
    :end-before: <!-- Include end warning -->
```

## Prevent issues with LXD and Docker

Running LXD and Docker on the same host can cause connectivity issues.
A common reason for these issues is that Docker sets the FORWARD policy to `drop`, which prevents LXD from forwarding traffic and thus causes the instances to lose network connectivity.
See [Docker on a router](https://docs.docker.com/network/iptables/#docker-on-a-router) for detailed information.

The easiest way to prevent such issues is to uninstall Docker from the system that runs LXD.
If that is not an option, use the following command to explicitly allow network traffic from your network bridge to your external network interface:

    iptables -I DOCKER-USER -i <network_bridge> -o <external_interface> -j ACCEPT
