package firewall

import (
	"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
)

// XTables is an implmentation of LXD firewall using {ip, ip6, eb}tables
type XTables struct {}

// Proxy
func (xt *XTables) proxyStop() (*device.RunConfig, error) {
	return nil, nil
}
func (xt *XTables) proxySetupNAT() {

}

// NIC bridged
func (xt *XTables) bridgeRemoveFilters(deviceConfig.Device) error {
	return nil
}
func (xt *XTables) bridgeSetFilters(deviceConfig.Device) error {
	return nil
}

// Network
func (xt *XTables) networkSetup(map[string]string) error {
	return nil
}
func (xt *XTables) networkStop() error {
	return nil
}
