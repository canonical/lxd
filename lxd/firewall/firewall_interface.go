package firewall

import (
	"net"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	drivers "github.com/lxc/lxd/lxd/firewall/drivers"
)

// Firewall represents a LXD firewall.
type Firewall interface {
	String() string
	Compat() (bool, error)

	NetworkSetup(networkName string, opts drivers.Opts) error
	NetworkClear(networkName string, delete bool, ipVersions []uint) error
	NetworkApplyACLRules(networkName string, rules []drivers.ACLRule) error

	InstanceSetupBridgeFilter(projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, IPv4 net.IP, IPv6 net.IP) error
	InstanceClearBridgeFilter(projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, IPv4 net.IP, IPv6 net.IP) error

	InstanceSetupProxyNAT(projectName string, instanceName string, deviceName string, listen *deviceConfig.ProxyAddress, connect *deviceConfig.ProxyAddress) error
	InstanceClearProxyNAT(projectName string, instanceName string, deviceName string) error

	InstanceSetupRPFilter(projectName string, instanceName string, deviceName string, hostName string) error
	InstanceClearRPFilter(projectName string, instanceName string, deviceName string) error
}
