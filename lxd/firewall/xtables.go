package firewall

import (
	"fmt"

	"github.com/lxc/lxd/lxd/iptables"
)

// Proxy
func ProxyStop() (*device.RunConfig, error) {
	return nil, nil
}
func ProxySetupNAT() {

}

// NIC bridged
func BridgeRemoveFilters(deviceConfig.Device) error {
	return nil
}
func BridgeSetFilters(deviceConfig.Device) error {
	return nil
}

// Network
func NetworkSetup(map[string]string) error {
	return nil
}
func NetworkStop() error {
	return nil
}
