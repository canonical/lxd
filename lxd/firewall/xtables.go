package firewall

import (
	"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
)

// XTables is an implmentation of LXD firewall using {ip, ip6, eb}tables
type XTables struct {}

// Lower-level clear functions
func (xt *XTables) NetworkClear(protocol string, comment string, table string) error {
	return nil
}
func (xt *XTables) ContainerClear(protocol string, comment string, table string) error {
	return nil
}

// Proxy
func (xt *XTables) ProxyStop() (*device.RunConfig, error) {
	return nil, nil
}
func (xt *XTables) ProxySetupNAT() {

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
