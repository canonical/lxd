package firewall

import (
	"fmt"
	"github.com/lxc/lxd/lxd/iptables"
	//"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
)

// XTables is an implmentation of LXD firewall using {ip, ip6, eb}tables
type XTables struct {}

// Lower-level clear functions

// NetworkClear removes network rules.
func (xt *XTables) NetworkClear(protocol string, comment string, table string) error {
	return iptables.NetworkClear(protocol, comment, table)
}

// ContainerClear removes container rules.
func (xt *XTables) ContainerClear(protocol string, comment string, table string) error {
	return iptables.ContainerClear(protocol, comment, table)
}

// Proxy
func (xt *XTables) ProxySetupNAT(ipv string, IPAddr string, comment string, connType, address, port string, cPort string) error {
	if IPAddr != "" {
		err := iptables.ContainerPrepend(ipv, comment, "nat", "PREROUTING", "-p", connType, "--destination", address, "--dport", port, "-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%s", IPAddr, cPort))
		if err != nil {
			return err
		}

		err = iptables.ContainerPrepend(ipv, comment, "nat", "OUTPUT", "-p", connType, "--destination", address, "--dport", port, "-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%s", IPAddr, cPort))
		if err != nil {
			return err
		}
	}

	return nil
}

// NIC bridged
func (xt *XTables) BridgeRemoveFilters(deviceConfig.Device) error {
	return nil
}
func (xt *XTables) BridgeSetFilters(deviceConfig.Device) error {
	return nil
}

// Network
func (xt *XTables) NetworkSetup(map[string]string) error {
	return nil
}
