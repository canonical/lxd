package firewall

import (
	"fmt"

	"github.com/lxc/lxd/lxd/iptables"
)

// NetworkAppend adds a network rule at end of ruleset.
func NetworkAppend(protocol string, comment string, table string, chain string,
    rule ...string) error {
    return iptables.NetworkAppend(protocol, fmt.Sprintf("LXD network %s", comment),
        table, chain, rule...)
}

// NetworkPrepend adds a network rule at start of ruleset.
func NetworkPrepend(protocol string, comment string, table string, chain string,
    rule ...string) error {
    return iptables.NetworkPrepend(protocol, fmt.Sprintf("LXD network %s", comment),
        table, chain, rule...)
}

// NetworkClear removes network rules.
func NetworkClear(protocol string, comment string, table string) error {
    return iptables.NetworkClear(protocol, fmt.Sprintf("LXD network %s", comment),
        table)
}

// ContainerPrepend adds container rule at start of ruleset.
func ContainerPrepend(protocol string, comment string, table string,
    chain string, rule ...string) error {
    return iptables.ContainerPrepend(protocol, fmt.Sprintf("LXD container %s", comment),
        table, chain, rule...)
}

// ContainerClear removes container rules.
func ContainerClear(protocol string, comment string, table string) error {
    return iptables.ContainerClear(protocol, fmt.Sprintf("LXD container %s", comment),
        table)
}
