package firewall

import (
	"net"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/firewall/drivers"
	"github.com/lxc/lxd/shared/api"
)

// Firewall represents a LXD firewall.
type Firewall interface {
	String() string
	Compat() (bool, error)
	Info() drivers.Info

	NetworkSetupForwardingPolicy(networkName string, ipVersion uint, allow bool) error
	NetworkSetupOutboundNAT(networkName string, subnet *net.IPNet, srcIP net.IP, append bool) error
	NetworkSetupDHCPDNSAccess(networkName string, ipVersion uint) error
	NetworkSetupDHCPv4Checksum(networkName string) error
	NetworkClear(networkName string, ipVersion uint) error

	InstanceSetupBridgeFilter(projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, IPv4 net.IP, IPv6 net.IP) error
	InstanceClearBridgeFilter(projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, IPv4 net.IP, IPv6 net.IP) error

	InstanceSetupProxyNAT(projectName string, instanceName string, deviceName string, listen *deviceConfig.ProxyAddress, connect *deviceConfig.ProxyAddress) error
	InstanceClearProxyNAT(projectName string, instanceName string, deviceName string) error

	InstanceSetupRPFilter(projectName string, instanceName string, deviceName string, hostName string) error
	InstanceClearRPFilter(projectName string, instanceName string, deviceName string) error

	ACLNetworkSetup(networkName string, intRouterIPs []*net.IPNet, dnsIPs []net.IP, acls map[int64]*api.NetworkACL) error
	ACLInstanceDevicePortSetup(networkName string, instanceUUID string, deviceName string, portName string, logName string, ingressAction string, ingressLogged bool, egressAction string, egressLogged bool) error
	ACLInstanceDevicePortDelete(networkName string, instanceUUID string, deviceName string, portName string) error
}
