package firewall

import (
	"context"
	"net"

	"github.com/canonical/lxd/lxd/firewall/drivers"
)

// Firewall represents a LXD firewall.
type Firewall interface {
	String() string
	Compat() (bool, error)

	NetworkSetup(ctx context.Context, networkName string, ip4Address net.IP, ip6Address net.IP, opts drivers.Opts) error
	NetworkClear(ctx context.Context, networkName string, remove bool, ipVersions []uint) error
	NetworkApplyACLRules(ctx context.Context, networkName string, rules []drivers.ACLRule) error
	NetworkApplyForwards(ctx context.Context, networkName string, rules []drivers.AddressForward) error

	InstanceSetupBridgeFilter(ctx context.Context, projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, IPv4Nets []*net.IPNet, IPv6Nets []*net.IPNet, parentManaged bool) error
	InstanceClearBridgeFilter(ctx context.Context, projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, IPv4Nets []*net.IPNet, IPv6Nets []*net.IPNet) error

	InstanceSetupProxyNAT(ctx context.Context, projectName string, instanceName string, deviceName string, forward *drivers.AddressForward) error
	InstanceClearProxyNAT(ctx context.Context, projectName string, instanceName string, deviceName string) error

	InstanceSetupRPFilter(ctx context.Context, projectName string, instanceName string, deviceName string, hostName string) error
	InstanceClearRPFilter(ctx context.Context, projectName string, instanceName string, deviceName string) error

	InstanceSetupNetPrio(ctx context.Context, projectName string, instanceName string, deviceName string, netPrio uint32) error
	InstanceClearNetPrio(ctx context.Context, projectName string, instanceName string, deviceName string) error
}
