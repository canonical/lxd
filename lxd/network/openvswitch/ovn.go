package openvswitch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/linux"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/dnsutil"
)

// OVNRouter OVN router name.
type OVNRouter string

// OVNRouterPort OVN router port name.
type OVNRouterPort string

// OVNSwitch OVN switch name.
type OVNSwitch string

// OVNSwitchPort OVN switch port name.
type OVNSwitchPort string

// OVNSwitchPortUUID OVN switch port UUID.
type OVNSwitchPortUUID string

// OVNChassisGroup OVN HA chassis group name.
type OVNChassisGroup string

// OVNDNSUUID OVN DNS record UUID.
type OVNDNSUUID string

// OVNDHCPOptionsUUID DHCP Options set UUID.
type OVNDHCPOptionsUUID string

// OVNPortGroup OVN port group name.
type OVNPortGroup string

// OVNPortGroupUUID OVN port group UUID.
type OVNPortGroupUUID string

// OVNLoadBalancer OVN load balancer name.
type OVNLoadBalancer string

// OVNAddressSet OVN address set for ACLs.
type OVNAddressSet string

// OVNIPAllocationOpts defines IP allocation settings that can be applied to a logical switch.
type OVNIPAllocationOpts struct {
	PrefixIPv4  *net.IPNet
	PrefixIPv6  *net.IPNet
	ExcludeIPv4 []shared.IPRange
}

// OVNIPv6AddressMode IPv6 router advertisement address mode.
type OVNIPv6AddressMode string

// OVNIPv6AddressModeSLAAC IPv6 SLAAC mode.
const OVNIPv6AddressModeSLAAC OVNIPv6AddressMode = "slaac"

// OVNIPv6AddressModeDHCPStateful IPv6 DHCPv6 stateful mode.
const OVNIPv6AddressModeDHCPStateful OVNIPv6AddressMode = "dhcpv6_stateful"

// OVNIPv6AddressModeDHCPStateless IPv6 DHCPv6 stateless mode.
const OVNIPv6AddressModeDHCPStateless OVNIPv6AddressMode = "dhcpv6_stateless"

// OVN External ID names used by LXD.
const ovnExtIDLXDSwitch = "lxd_switch"
const ovnExtIDLXDSwitchPort = "lxd_switch_port"
const ovnExtIDLXDProjectID = "lxd_project_id"
const ovnExtIDLXDPortGroup = "lxd_port_group"
const ovnExtIDLXDLocation = "lxd_location"

// OVNIPv6RAOpts IPv6 router advertisements options that can be applied to a router.
type OVNIPv6RAOpts struct {
	SendPeriodic       bool
	AddressMode        OVNIPv6AddressMode
	MinInterval        time.Duration
	MaxInterval        time.Duration
	RecursiveDNSServer net.IP
	DNSSearchList      []string
	MTU                uint32
}

// OVNDHCPOptsSet is an existing DHCP options set in the northbound database.
type OVNDHCPOptsSet struct {
	UUID OVNDHCPOptionsUUID
	CIDR *net.IPNet
}

// OVNDHCPv4Opts IPv4 DHCP options that can be applied to a switch port.
type OVNDHCPv4Opts struct {
	ServerID           net.IP
	ServerMAC          net.HardwareAddr
	Router             net.IP
	RecursiveDNSServer []net.IP
	DomainName         string
	LeaseTime          time.Duration
	MTU                uint32
	Netmask            string
}

// OVNDHCPv6Opts IPv6 DHCP option set that can be created (and then applied to a switch port by resulting ID).
type OVNDHCPv6Opts struct {
	ServerID           net.HardwareAddr
	RecursiveDNSServer []net.IP
	DNSSearchList      []string
}

// OVNSwitchPortOpts options that can be applied to a swich port.
type OVNSwitchPortOpts struct {
	MAC          net.HardwareAddr   // Optional, if nil will be set to dynamic.
	IPs          []net.IP           // Optional, static IPs for port, cannot be provided if DynamicIPs is true.
	DynamicIPs   bool               // Request dynamic IP allocations for both IPv4 and IPv6 if available.
	DHCPv4OptsID OVNDHCPOptionsUUID // Optional, if empty, no DHCPv4 enabled on port.
	DHCPv6OptsID OVNDHCPOptionsUUID // Optional, if empty, no DHCPv6 enabled on port.
	Parent       OVNSwitchPort      // Optional, if set a nested port is created.
	VLAN         uint16             // Optional, use with Parent to request a specific VLAN for nested port.
	Location     string             // Optional, use to indicate the name of the LXD server this port is bound to.
}

// OVNACLRule represents an ACL rule that can be added to a logical switch or port group.
type OVNACLRule struct {
	Direction string // Either "from-lport" or "to-lport".
	Action    string // Either "allow-related", "allow", "drop", or "reject".
	Match     string // Match criteria. See OVN Southbound database's Logical_Flow table match column usage.
	Priority  int    // Priority (between 0 and 32767, inclusive). Higher values take precedence.
	Log       bool   // Whether or not to log matched packets.
	LogName   string // Log label name (requires Log be true).
}

// OVNLoadBalancerTarget represents an OVN load balancer Virtual IP target.
type OVNLoadBalancerTarget struct {
	Address net.IP
	Port    uint64
}

// OVNLoadBalancerVIP represents a OVN load balancer Virtual IP entry.
type OVNLoadBalancerVIP struct {
	Protocol      string // Either "tcp" or "udp". But only applies to port based VIPs.
	ListenAddress net.IP
	ListenPort    uint64
	Targets       []OVNLoadBalancerTarget
}

// OVNRouterRoute represents a static route added to a logical router.
type OVNRouterRoute struct {
	Prefix  net.IPNet
	NextHop net.IP
	Port    OVNRouterPort
	Discard bool
}

// OVNRouterPolicy represents a router policy.
type OVNRouterPolicy struct {
	Priority int
	Match    string
	Action   string
	NextHop  net.IP
}

// OVNRouterPeering represents a the configuration of a peering connection between two OVN logical routers.
type OVNRouterPeering struct {
	LocalRouter        OVNRouter
	LocalRouterPort    OVNRouterPort
	LocalRouterPortMAC net.HardwareAddr
	LocalRouterPortIPs []net.IPNet
	LocalRouterRoutes  []net.IPNet

	TargetRouter        OVNRouter
	TargetRouterPort    OVNRouterPort
	TargetRouterPortMAC net.HardwareAddr
	TargetRouterPortIPs []net.IPNet
	TargetRouterRoutes  []net.IPNet
}

// NewOVN initialises new OVN client wrapper with the connection set in network.ovn.northbound_connection config.
func NewOVN(nbConnection string, sslSettings func() (sslCACert string, sslClientCert string, sslClientKey string)) (*OVN, error) {
	// Get database connection strings.
	sbConnection, err := NewOVS().OVNSouthboundDBRemoteAddress()
	if err != nil {
		return nil, fmt.Errorf("Failed to get OVN southbound connection string: %w", err)
	}

	// Create the OVN struct.
	client := &OVN{
		nbDBAddr: nbConnection,
		sbDBAddr: sbConnection,
	}

	// If using SSL, then get the CA and client key pair.
	if strings.Contains(nbConnection, "ssl:") {
		sslCACert, sslClientCert, sslClientKey := sslSettings()

		if sslCACert == "" {
			content, err := os.ReadFile("/etc/ovn/ovn-central.crt")
			if err != nil {
				if os.IsNotExist(err) {
					return nil, errors.New("OVN configured to use SSL but no SSL CA certificate defined")
				}

				return nil, err
			}

			sslCACert = string(content)
		}

		if sslClientCert == "" {
			content, err := os.ReadFile("/etc/ovn/cert_host")
			if err != nil {
				if os.IsNotExist(err) {
					return nil, errors.New("OVN configured to use SSL but no SSL client certificate defined")
				}

				return nil, err
			}

			sslClientCert = string(content)
		}

		if sslClientKey == "" {
			content, err := os.ReadFile("/etc/ovn/key_host")
			if err != nil {
				if os.IsNotExist(err) {
					return nil, errors.New("OVN configured to use SSL but no SSL client key defined")
				}

				return nil, err
			}

			sslClientKey = string(content)
		}

		client.sslCACert = sslCACert
		client.sslClientCert = sslClientCert
		client.sslClientKey = sslClientKey
	}

	return client, nil
}

// OVN command wrapper.
type OVN struct {
	nbDBAddr string
	sbDBAddr string

	sslCACert     string
	sslClientCert string
	sslClientKey  string
}

// SetNorthboundDBAddress sets the address that runs the OVN northbound databases.
func (o *OVN) SetNorthboundDBAddress(addr string) {
	o.nbDBAddr = addr
}

// getNorthboundDB returns connection string to use for northbound database.
func (o *OVN) getNorthboundDB() string {
	if o.nbDBAddr == "" {
		return "unix:/var/run/ovn/ovnnb_db.sock"
	}

	return o.nbDBAddr
}

// SetSouthboundDBAddress sets the address that runs the OVN northbound databases.
func (o *OVN) SetSouthboundDBAddress(addr string) {
	o.sbDBAddr = addr
}

// getSouthboundDB returns connection string to use for northbound database.
func (o *OVN) getSouthboundDB() string {
	if o.sbDBAddr == "" {
		return "unix:/var/run/ovn/ovnsb_db.sock"
	}

	return o.sbDBAddr
}

// sbctl executes ovn-sbctl with arguments to connect to wrapper's southbound database.
func (o *OVN) sbctl(args ...string) (string, error) {
	return o.xbctl(true, args...)
}

// nbctl executes ovn-nbctl with arguments to connect to wrapper's northbound database.
func (o *OVN) nbctl(args ...string) (string, error) {
	return o.xbctl(false, append([]string{"--wait=sb"}, args...)...)
}

// xbctl optionally executes either ovn-nbctl or ovn-sbctl with arguments to connect to wrapper's northbound or southbound database.
func (o *OVN) xbctl(southbound bool, extraArgs ...string) (string, error) {
	dbAddr := o.getNorthboundDB()
	cmd := "ovn-nbctl"
	if southbound {
		dbAddr = o.getSouthboundDB()
		cmd = "ovn-sbctl"
	}

	after, ok := strings.CutPrefix(dbAddr, "unix:")
	if ok {
		dbAddr = "unix:" + shared.HostPathFollow(after)
	}

	// Figure out args.
	args := []string{"--timeout=10", "--db", dbAddr}

	// Handle SSL args.
	files := []*os.File{}
	if strings.Contains(dbAddr, "ssl:") {
		// Handle client certificate.
		clientCertFile, err := linux.CreateMemfd([]byte(o.sslClientCert))
		if err != nil {
			return "", err
		}

		defer clientCertFile.Close()
		files = append(files, clientCertFile)

		// Handle client key.
		clientKeyFile, err := linux.CreateMemfd([]byte(o.sslClientKey))
		if err != nil {
			return "", err
		}

		defer clientKeyFile.Close()
		files = append(files, clientKeyFile)

		// Handle CA certificate.
		caCertFile, err := linux.CreateMemfd([]byte(o.sslCACert))
		if err != nil {
			return "", err
		}

		defer caCertFile.Close()
		files = append(files, caCertFile)

		args = append(args,
			"-c", "/proc/self/fd/3",
			"-p", "/proc/self/fd/4",
			"-C", "/proc/self/fd/5",
		)
	}

	args = append(args, extraArgs...)
	return shared.RunCommandInheritFds(context.Background(), files, cmd, args...)
}

// LogicalRouterAdd adds a named logical router.
func (o *OVN) LogicalRouterAdd(routerName OVNRouter, mayExist bool) error {
	args := []string{}

	if mayExist {
		args = append(args, "--may-exist")
	}

	_, err := o.nbctl(append(args, "lr-add", string(routerName))...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterDelete deletes a named logical router.
func (o OVN) LogicalRouterDelete(routerName OVNRouter) error {
	_, err := o.nbctl("--if-exists", "lr-del", string(routerName))
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterSNATAdd adds an SNAT rule to a logical router to translate packets from intNet to extIP.
func (o *OVN) LogicalRouterSNATAdd(routerName OVNRouter, intNet *net.IPNet, extIP net.IP, mayExist bool) error {
	args := []string{}

	if mayExist {
		args = append(args, "--if-exists", "lr-nat-del", string(routerName), "snat", extIP.String(), "--")
	}

	_, err := o.nbctl(append(args, "lr-nat-add", string(routerName), "snat", extIP.String(), intNet.String())...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterDNATSNATDeleteAll deletes all DNAT_AND_SNAT rules from a logical router.
func (o *OVN) LogicalRouterDNATSNATDeleteAll(routerName OVNRouter) error {
	_, err := o.nbctl("--if-exists", "lr-nat-del", string(routerName), "dnat_and_snat")
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterSNATDeleteAll deletes all SNAT rules from a logical router.
func (o *OVN) LogicalRouterSNATDeleteAll(routerName OVNRouter) error {
	_, err := o.nbctl("--if-exists", "lr-nat-del", string(routerName), "snat")
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterDNATSNATAdd adds a DNAT_AND_SNAT rule to a logical router to translate packets from extIP to intIP.
func (o *OVN) LogicalRouterDNATSNATAdd(routerName OVNRouter, extIP net.IP, intIP net.IP, stateless bool, mayExist bool) error {
	if mayExist {
		// There appears to be a bug in ovn-nbctl where running lr-nat-del as part of the same command as
		// lr-nat-add doesn't take account the changes by lr-nat-del, and so you can end up with errors
		// if a NAT entry already exists. So we run them as separate command invocations.
		// There can be left over dnat_and_snat entries if an instance was stopped when the ovn-nb DB
		// was not reachable.
		_, err := o.nbctl("--if-exists", "lr-nat-del", string(routerName), "dnat_and_snat", extIP.String())
		if err != nil {
			return err
		}
	}

	args := []string{}

	if stateless {
		args = append(args, "--stateless")
	}

	_, err := o.nbctl(append(args, "lr-nat-add", string(routerName), "dnat_and_snat", extIP.String(), intIP.String())...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterDNATSNATDelete deletes a DNAT_AND_SNAT rule from a logical router.
func (o *OVN) LogicalRouterDNATSNATDelete(routerName OVNRouter, extIPs ...net.IP) error {
	args := []string{}

	for _, extIP := range extIPs {
		if len(args) > 0 {
			args = append(args, "--")
		}

		args = append(args, "--if-exists", "lr-nat-del", string(routerName), "dnat_and_snat", extIP.String())
	}

	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterRouteAdd adds a static route to the logical router.
func (o *OVN) LogicalRouterRouteAdd(routerName OVNRouter, mayExist bool, routes ...OVNRouterRoute) error {
	args := []string{}

	for _, route := range routes {
		if len(args) > 0 {
			args = append(args, "--")
		}

		if mayExist {
			args = append(args, "--may-exist")
		}

		args = append(args, "lr-route-add", string(routerName), route.Prefix.String())

		if route.Discard {
			args = append(args, "discard")
		} else {
			args = append(args, route.NextHop.String())
		}

		if route.Port != "" {
			args = append(args, string(route.Port))
		}
	}

	if len(args) > 0 {
		_, err := o.nbctl(args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// LogicalRouterRouteDelete deletes a static route from the logical router.
func (o *OVN) LogicalRouterRouteDelete(routerName OVNRouter, prefixes ...net.IPNet) error {
	args := []string{}

	// Delete specific destination routes on router.
	for _, prefix := range prefixes {
		if len(args) > 0 {
			args = append(args, "--")
		}

		args = append(args, "--if-exists", "lr-route-del", string(routerName), prefix.String())
	}

	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterPortAdd adds a named logical router port to a logical router.
func (o *OVN) LogicalRouterPortAdd(routerName OVNRouter, portName OVNRouterPort, mac net.HardwareAddr, gatewayMTU uint32, ipAddr []*net.IPNet, mayExist bool) error {
	if mayExist {
		// Check if it exists and update addresses.
		_, err := o.nbctl("list", "Logical_Router_Port", string(portName))
		if err == nil {
			// Router port exists.
			ips := make([]string, 0, len(ipAddr))
			for _, ip := range ipAddr {
				ips = append(ips, ip.String())
			}

			_, err := o.nbctl("set", "Logical_Router_Port", string(portName),
				`networks="`+strings.Join(ips, `","`)+`"`,
				`mac="`+mac.String()+`"`,
				"options:gateway_mtu="+strconv.FormatUint(uint64(gatewayMTU), 10),
			)
			if err != nil {
				return err
			}

			return nil
		}
	}

	args := []string{"lrp-add", string(routerName), string(portName), mac.String()}
	for _, ipNet := range ipAddr {
		args = append(args, ipNet.String())
	}

	args = append(args, "--", "set", "Logical_Router_Port", string(portName),
		"options:gateway_mtu="+strconv.FormatUint(uint64(gatewayMTU), 10),
	)

	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterPortDelete deletes a named logical router port from a logical router.
func (o *OVN) LogicalRouterPortDelete(portName OVNRouterPort) error {
	_, err := o.nbctl("--if-exists", "lrp-del", string(portName))
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterPortSetIPv6Advertisements sets the IPv6 router advertisement options on a router port.
func (o *OVN) LogicalRouterPortSetIPv6Advertisements(portName OVNRouterPort, opts *OVNIPv6RAOpts) error {
	args := []string{"set", "logical_router_port", string(portName),
		"ipv6_ra_configs:send_periodic=" + strconv.FormatBool(opts.SendPeriodic),
	}

	var removeRAConfigKeys []string //nolint:prealloc

	if opts.AddressMode != "" {
		args = append(args, "ipv6_ra_configs:address_mode="+string(opts.AddressMode))
	} else {
		removeRAConfigKeys = append(removeRAConfigKeys, "address_mode")
	}

	if opts.MaxInterval > 0 {
		args = append(args, "ipv6_ra_configs:max_interval="+strconv.FormatInt(int64(opts.MaxInterval.Seconds()), 10))
	} else {
		removeRAConfigKeys = append(removeRAConfigKeys, "max_interval")
	}

	if opts.MinInterval > 0 {
		args = append(args, "ipv6_ra_configs:min_interval="+strconv.FormatInt(int64(opts.MinInterval.Seconds()), 10))
	} else {
		removeRAConfigKeys = append(removeRAConfigKeys, "min_interval")
	}

	if opts.MTU > 0 {
		args = append(args, "ipv6_ra_configs:mtu="+strconv.FormatUint(uint64(opts.MTU), 10))
	} else {
		removeRAConfigKeys = append(removeRAConfigKeys, "mtu")
	}

	if len(opts.DNSSearchList) > 0 {
		args = append(args, "ipv6_ra_configs:dnssl="+strings.Join(opts.DNSSearchList, ","))
	} else {
		removeRAConfigKeys = append(removeRAConfigKeys, "dnssl")
	}

	if opts.RecursiveDNSServer != nil {
		args = append(args, "ipv6_ra_configs:rdnss="+opts.RecursiveDNSServer.String())
	} else {
		removeRAConfigKeys = append(removeRAConfigKeys, "rdnss")
	}

	// Clear any unused keys first.
	if len(removeRAConfigKeys) > 0 {
		removeArgs := append([]string{"remove", "logical_router_port", string(portName), "ipv6_ra_configs"}, removeRAConfigKeys...)
		_, err := o.nbctl(removeArgs...)
		if err != nil {
			return err
		}
	}

	// Configure IPv6 Router Advertisements.
	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterPortDeleteIPv6Advertisements removes the IPv6 RA announcement settings from a router port.
func (o *OVN) LogicalRouterPortDeleteIPv6Advertisements(portName OVNRouterPort) error {
	// Delete IPv6 Router Advertisements.
	_, err := o.nbctl("clear", "logical_router_port", string(portName), "ipv6_ra_configs")
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterPortLinkChassisGroup links a logical router port to a HA chassis group.
func (o *OVN) LogicalRouterPortLinkChassisGroup(portName OVNRouterPort, haChassisGroupName OVNChassisGroup) error {
	chassisGroupID, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--columns=_uuid", "find", "ha_chassis_group", "name="+string(haChassisGroupName))
	if err != nil {
		return err
	}

	chassisGroupID = strings.TrimSpace(chassisGroupID)

	if chassisGroupID == "" {
		return errors.New("Chassis group not found")
	}

	_, err = o.nbctl("set", "logical_router_port", string(portName), "ha_chassis_group="+chassisGroupID)
	if err != nil {
		return err
	}

	return nil
}

// LogicalSwitchAdd adds a named logical switch.
// If mayExist is true, then an existing resource of the same name is not treated as an error.
func (o *OVN) LogicalSwitchAdd(switchName OVNSwitch, mayExist bool) error {
	args := []string{}

	if mayExist {
		args = append(args, "--may-exist")
	}

	args = append(args, "ls-add", string(switchName))
	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalSwitchDelete deletes a named logical switch.
func (o *OVN) LogicalSwitchDelete(switchName OVNSwitch) error {
	args := []string{"--if-exists", "ls-del", string(switchName)}

	assocPortGroups, err := o.logicalSwitchFindAssociatedPortGroups(switchName)
	if err != nil {
		return err
	}

	for _, assocPortGroup := range assocPortGroups {
		args = append(args, "--", "destroy", "port_group", string(assocPortGroup))
	}

	_, err = o.nbctl(args...)
	if err != nil {
		return err
	}

	// Remove any existing DHCP options associated to switch.
	deleteDHCPRecords, err := o.LogicalSwitchDHCPOptionsGet(switchName)
	if err != nil {
		return err
	}

	if len(deleteDHCPRecords) > 0 {
		deleteDHCPUUIDs := make([]OVNDHCPOptionsUUID, 0, len(deleteDHCPRecords))
		for _, deleteDHCPRecord := range deleteDHCPRecords {
			deleteDHCPUUIDs = append(deleteDHCPUUIDs, deleteDHCPRecord.UUID)
		}

		err = o.LogicalSwitchDHCPOptionsDelete(switchName, deleteDHCPUUIDs...)
		if err != nil {
			return err
		}
	}

	err = o.logicalSwitchDNSRecordsDelete(switchName)
	if err != nil {
		return err
	}

	return nil
}

// logicalSwitchFindAssociatedPortGroups finds the port groups that are associated to the switch specified.
func (o *OVN) logicalSwitchFindAssociatedPortGroups(switchName OVNSwitch) ([]OVNPortGroup, error) {
	output, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--columns=name", "find", "port_group",
		"external_ids:"+string(ovnExtIDLXDSwitch)+"="+string(switchName),
	)
	if err != nil {
		return nil, err
	}

	lines := shared.SplitNTrimSpace(strings.TrimSpace(output), "\n", -1, true)
	portGroups := make([]OVNPortGroup, 0, len(lines))

	for _, line := range lines {
		portGroups = append(portGroups, OVNPortGroup(line))
	}

	return portGroups, nil
}

// logicalSwitchParseExcludeIPs parses the ips into OVN exclude_ips format.
func (o *OVN) logicalSwitchParseExcludeIPs(ips []shared.IPRange) ([]string, error) {
	excludeIPs := make([]string, 0, len(ips))
	for _, v := range ips {
		if v.Start == nil || v.Start.To4() == nil {
			return nil, errors.New("Invalid exclude IPv4 range start address")
		} else if v.End == nil {
			excludeIPs = append(excludeIPs, v.Start.String())
		} else {
			if v.End != nil && v.End.To4() == nil {
				return nil, errors.New("Invalid exclude IPv4 range end address")
			}

			excludeIPs = append(excludeIPs, v.Start.String()+".."+v.End.String())
		}
	}

	return excludeIPs, nil
}

// LogicalSwitchSetIPAllocation sets the IP allocation config on the logical switch.
func (o *OVN) LogicalSwitchSetIPAllocation(switchName OVNSwitch, opts *OVNIPAllocationOpts) error {
	var removeOtherConfigKeys []string //nolint:prealloc
	args := []string{"set", "logical_switch", string(switchName)}

	if opts.PrefixIPv4 != nil {
		args = append(args, "other_config:subnet="+opts.PrefixIPv4.String())
	} else {
		removeOtherConfigKeys = append(removeOtherConfigKeys, "subnet")
	}

	if opts.PrefixIPv6 != nil {
		args = append(args, "other_config:ipv6_prefix="+opts.PrefixIPv6.String())
	} else {
		removeOtherConfigKeys = append(removeOtherConfigKeys, "ipv6_prefix")
	}

	if len(opts.ExcludeIPv4) > 0 {
		excludeIPs, err := o.logicalSwitchParseExcludeIPs(opts.ExcludeIPv4)
		if err != nil {
			return err
		}

		args = append(args, "other_config:exclude_ips="+strings.Join(excludeIPs, " "))
	} else {
		removeOtherConfigKeys = append(removeOtherConfigKeys, "exclude_ips")
	}

	// Clear any unused keys first.
	if len(removeOtherConfigKeys) > 0 {
		removeArgs := append([]string{"remove", "logical_switch", string(switchName), "other_config"}, removeOtherConfigKeys...)
		_, err := o.nbctl(removeArgs...)
		if err != nil {
			return err
		}
	}

	// Only run command if at least one setting is specified.
	if len(args) > 3 {
		_, err := o.nbctl(args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// LogicalSwitchDHCPv4OptionsSet creates or updates a DHCPv4 option set associated with the specified switchName
// and subnet. If uuid is non-empty then the record that exists with that ID is updated, otherwise a new record
// is created.
func (o *OVN) LogicalSwitchDHCPv4OptionsSet(switchName OVNSwitch, uuid OVNDHCPOptionsUUID, subnet *net.IPNet, opts *OVNDHCPv4Opts) error {
	var err error

	if uuid != "" {
		_, err = o.nbctl("set", "dhcp_option", string(uuid),
			"external_ids:"+string(ovnExtIDLXDSwitch)+"="+string(switchName),
			"cidr="+subnet.String(),
		)
		if err != nil {
			return err
		}
	} else {
		uuidRaw, err := o.nbctl("create", "dhcp_option",
			"external_ids:"+string(ovnExtIDLXDSwitch)+"="+string(switchName),
			"cidr="+subnet.String(),
		)
		if err != nil {
			return err
		}

		uuid = OVNDHCPOptionsUUID(strings.TrimSpace(uuidRaw))
	}

	// We have to use dhcp-options-set-options rather than the command above as its the only way to allow the
	// domain_name option to be properly escaped.
	args := []string{"dhcp-options-set-options", string(uuid),
		"server_id=" + opts.ServerID.String(),
		"server_mac=" + opts.ServerMAC.String(),
		"lease_time=" + strconv.FormatInt(int64(opts.LeaseTime.Seconds()), 10),
	}

	if opts.Router != nil {
		args = append(args, "router="+opts.Router.String())
	}

	if opts.RecursiveDNSServer != nil {
		nsIPs := make([]string, 0, len(opts.RecursiveDNSServer))
		for _, nsIP := range opts.RecursiveDNSServer {
			if nsIP.To4() == nil {
				continue // Only include IPv4 addresses.
			}

			nsIPs = append(nsIPs, nsIP.String())
		}

		args = append(args, "dns_server={"+strings.Join(nsIPs, ",")+"}")
	}

	if opts.DomainName != "" {
		// Special quoting to allow domain names.
		args = append(args, `domain_name="`+opts.DomainName+`"`)
	}

	if opts.MTU > 0 {
		args = append(args, "mtu="+strconv.FormatUint(uint64(opts.MTU), 10))
	}

	if opts.Netmask != "" {
		args = append(args, "netmask="+opts.Netmask)
	}

	_, err = o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalSwitchDHCPv6OptionsSet creates or updates a DHCPv6 option set associated with the specified switchName
// and subnet. If uuid is non-empty then the record that exists with that ID is updated, otherwise a new record
// is created.
func (o *OVN) LogicalSwitchDHCPv6OptionsSet(switchName OVNSwitch, uuid OVNDHCPOptionsUUID, subnet *net.IPNet, opts *OVNDHCPv6Opts) error {
	var err error

	if uuid != "" {
		_, err = o.nbctl("set", "dhcp_option", string(uuid),
			fmt.Sprintf("external_ids:%s=%s", ovnExtIDLXDSwitch, switchName),
			fmt.Sprintf(`cidr="%s"`, subnet.String()), // Special quoting to allow IPv6 address.
		)
		if err != nil {
			return err
		}
	} else {
		uuidRaw, err := o.nbctl("create", "dhcp_option",
			fmt.Sprintf("external_ids:%s=%s", ovnExtIDLXDSwitch, switchName),
			fmt.Sprintf(`cidr="%s"`, subnet.String()), // Special quoting to allow IPv6 address.
		)
		if err != nil {
			return err
		}

		uuid = OVNDHCPOptionsUUID(strings.TrimSpace(uuidRaw))
	}

	// We have to use dhcp-options-set-options rather than the command above as its the only way to allow the
	// domain_name option to be properly escaped.
	args := []string{"dhcp-options-set-options", string(uuid),
		"server_id=" + opts.ServerID.String(),
	}

	if len(opts.DNSSearchList) > 0 {
		// Special quoting to allow domain names.
		args = append(args, fmt.Sprintf(`domain_search="%s"`, strings.Join(opts.DNSSearchList, ",")))
	}

	if opts.RecursiveDNSServer != nil {
		nsIPs := make([]string, 0, len(opts.RecursiveDNSServer))
		for _, nsIP := range opts.RecursiveDNSServer {
			if nsIP.To4() != nil {
				continue // Only include IPv6 addresses.
			}

			nsIPs = append(nsIPs, nsIP.String())
		}

		args = append(args, fmt.Sprintf("dns_server={%s}", strings.Join(nsIPs, ",")))
	}

	_, err = o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalSwitchDHCPOptionsGet retrieves the existing DHCP options defined for a logical switch.
func (o *OVN) LogicalSwitchDHCPOptionsGet(switchName OVNSwitch) ([]OVNDHCPOptsSet, error) {
	output, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--columns=_uuid,cidr", "find", "dhcp_options",
		fmt.Sprintf("external_ids:%s=%s", ovnExtIDLXDSwitch, switchName),
	)
	if err != nil {
		return nil, err
	}

	colCount := 2
	dhcpOpts := []OVNDHCPOptsSet{}
	output = strings.TrimSpace(output)
	if output != "" {
		for row := range strings.SplitSeq(output, "\n") {
			rowParts := strings.SplitN(row, ",", colCount)
			if len(rowParts) < colCount {
				return nil, errors.New("Too few columns in output")
			}

			_, cidr, err := net.ParseCIDR(rowParts[1])
			if err != nil {
				return nil, err
			}

			dhcpOpts = append(dhcpOpts, OVNDHCPOptsSet{
				UUID: OVNDHCPOptionsUUID(rowParts[0]),
				CIDR: cidr,
			})
		}
	}

	return dhcpOpts, nil
}

// LogicalSwitchDHCPOptionsDelete deletes the specified DHCP options defined for a switch.
func (o *OVN) LogicalSwitchDHCPOptionsDelete(switchName OVNSwitch, uuids ...OVNDHCPOptionsUUID) error {
	args := []string{}

	for _, uuid := range uuids {
		if len(args) > 0 {
			args = append(args, "--")
		}

		args = append(args, "destroy", "dhcp_options", string(uuid))
	}

	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// logicalSwitchDNSRecordsDelete deletes any DNS records defined for a switch.
func (o *OVN) logicalSwitchDNSRecordsDelete(switchName OVNSwitch) error {
	uuids, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--columns=_uuid", "find", "dns",
		fmt.Sprintf("external_ids:%s=%s", ovnExtIDLXDSwitch, switchName),
	)
	if err != nil {
		return err
	}

	args := []string{}

	for _, uuid := range shared.SplitNTrimSpace(strings.TrimSpace(uuids), "\n", -1, true) {
		if len(args) > 0 {
			args = append(args, "--")
		}

		args = append(args, "destroy", "dns", uuid)
	}

	if len(args) > 0 {
		_, err = o.nbctl(args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// LogicalSwitchSetACLRules applies a set of rules to the specified logical switch. Any existing rules are removed.
func (o *OVN) LogicalSwitchSetACLRules(switchName OVNSwitch, aclRules ...OVNACLRule) error {
	// Remove any existing rules assigned to the entity.
	args := []string{"clear", "logical_switch", string(switchName), "acls"}

	// Add new rules.
	externalIDs := map[string]string{
		ovnExtIDLXDSwitch: string(switchName),
	}

	args = o.aclRuleAddAppendArgs(args, "logical_switch", string(switchName), externalIDs, nil, aclRules...)

	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// logicalSwitchPortACLRules returns the ACL rule UUIDs belonging to a logical switch port.
func (o *OVN) logicalSwitchPortACLRules(portName OVNSwitchPort) ([]string, error) {
	// Remove any existing rules assigned to the entity.
	output, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--columns=_uuid", "find", "acl", "external_ids:"+string(ovnExtIDLXDSwitchPort)+"="+string(portName))
	if err != nil {
		return nil, err
	}

	ruleUUIDs := shared.SplitNTrimSpace(strings.TrimSpace(output), "\n", -1, true)

	return ruleUUIDs, nil
}

// LogicalSwitchPorts returns a map of logical switch ports (name and UUID) for a switch.
// Includes non-instance ports, such as the router port.
func (o *OVN) LogicalSwitchPorts(switchName OVNSwitch) (map[OVNSwitchPort]OVNSwitchPortUUID, error) {
	output, err := o.nbctl("lsp-list", string(switchName))
	if err != nil {
		return nil, err
	}

	lines := shared.SplitNTrimSpace(strings.TrimSpace(output), "\n", -1, true)
	ports := make(map[OVNSwitchPort]OVNSwitchPortUUID, len(lines))

	for _, line := range lines {
		// E.g. "c709c4a8-ef3f-4ffe-a45a-c75295eb2698 (lxd-net3-instance-fc933d65-0900-46b0-b5f2-4d323342e755-eth0)"
		fields := strings.Fields(line)

		if len(fields) != 2 {
			return nil, fmt.Errorf("Unrecognised switch port item output %q", line)
		}

		portUUID := OVNSwitchPortUUID(fields[0])
		portName := OVNSwitchPort(strings.TrimPrefix(strings.TrimSuffix(fields[1], ")"), "("))
		ports[portName] = portUUID
	}

	return ports, nil
}

// LogicalSwitchIPs returns a list of IPs associated to each port connected to switch.
func (o *OVN) LogicalSwitchIPs(switchName OVNSwitch) (map[OVNSwitchPort][]net.IP, error) {
	output, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--columns=name,addresses,dynamic_addresses", "find", "logical_switch_port",
		fmt.Sprintf("external_ids:%s=%s", ovnExtIDLXDSwitch, switchName),
	)
	if err != nil {
		return nil, err
	}

	lines := shared.SplitNTrimSpace(strings.TrimSpace(output), "\n", -1, true)
	portIPs := make(map[OVNSwitchPort][]net.IP, len(lines))

	for _, line := range lines {
		fields := shared.SplitNTrimSpace(line, ",", -1, true)
		portName := OVNSwitchPort(fields[0])
		ips := make([]net.IP, 0, len(fields))

		// Parse all IPs mentioned in addresses and dynamic_addresses fields.
		for i := 1; i < len(fields); i++ {
			for _, address := range shared.SplitNTrimSpace(fields[i], " ", -1, true) {
				ip := net.ParseIP(address)
				if ip != nil {
					ips = append(ips, ip)
				}
			}
		}

		portIPs[portName] = ips
	}

	return portIPs, nil
}

// LogicalSwitchPortUUID returns the logical switch port UUID or empty string if port doesn't exist.
func (o *OVN) LogicalSwitchPortUUID(portName OVNSwitchPort) (OVNSwitchPortUUID, error) {
	portInfo, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--columns=_uuid,name", "find", "logical_switch_port", "name="+string(portName))
	if err != nil {
		return "", err
	}

	portParts := shared.SplitNTrimSpace(portInfo, ",", 2, false)
	if len(portParts) == 2 {
		if portParts[1] == string(portName) {
			return OVNSwitchPortUUID(portParts[0]), nil
		}
	}

	return "", nil
}

// LogicalSwitchPortAdd adds a named logical switch port to a logical switch, and sets options if provided.
// If mayExist is true, then an existing resource of the same name is not treated as an error.
func (o *OVN) LogicalSwitchPortAdd(switchName OVNSwitch, portName OVNSwitchPort, opts *OVNSwitchPortOpts, mayExist bool) error {
	args := []string{}

	if mayExist {
		args = append(args, "--may-exist")
	}

	// Add switch port.
	args = append(args, "lsp-add", string(switchName), string(portName))

	// Set switch port options if supplied.
	if opts != nil {
		// Created nested VLAN port if requested.
		if opts.Parent != "" {
			args = append(args, string(opts.Parent), strconv.FormatUint(uint64(opts.VLAN), 10))
		}

		var addresses []string

		if opts.MAC != nil {
			addresses = append(addresses, opts.MAC.String())
		}

		for _, ip := range opts.IPs {
			addresses = append(addresses, ip.String())
		}

		if opts.DynamicIPs {
			if len(opts.IPs) > 0 {
				return errors.New("Cannot specify static IPs and dynamic IPs at the same time")
			}

			addresses = append(addresses, "dynamic")
		}

		if len(addresses) > 0 {
			args = append(args, "--", "lsp-set-addresses", string(portName), strings.Join(addresses, " "))
		}

		if opts.DHCPv4OptsID != "" {
			args = append(args, "--", "lsp-set-dhcpv4-options", string(portName), string(opts.DHCPv4OptsID))
		}

		if opts.DHCPv6OptsID != "" {
			args = append(args, "--", "lsp-set-dhcpv6-options", string(portName), string(opts.DHCPv6OptsID))
		}

		if opts.Location != "" {
			args = append(args, "--", "set", "logical_switch_port", string(portName), fmt.Sprintf("external_ids:%s=%s", ovnExtIDLXDLocation, opts.Location))
		}
	}

	args = append(args, "--", "set", "logical_switch_port", string(portName), fmt.Sprintf("external_ids:%s=%s", ovnExtIDLXDSwitch, switchName))

	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalSwitchPortIPs returns a list of IPs for a switch port.
func (o *OVN) LogicalSwitchPortIPs(portName OVNSwitchPort) ([]net.IP, error) {
	addressesRaw, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--columns=addresses,dynamic_addresses", "find", "logical_switch_port", "name="+string(portName))
	if err != nil {
		return nil, err
	}

	addresses := strings.Split(strings.Replace(strings.TrimSpace(addressesRaw), ",", " ", 1), " ")
	ips := make([]net.IP, 0, len(addresses))

	for _, address := range addresses {
		ip := net.ParseIP(address)
		if ip != nil {
			ips = append(ips, ip)
		}
	}

	return ips, nil
}

// LogicalSwitchPortDynamicIPs returns a list of dynamc IPs for a switch port.
func (o *OVN) LogicalSwitchPortDynamicIPs(portName OVNSwitchPort) ([]net.IP, error) {
	dynamicAddressesRaw, err := o.nbctl("get", "logical_switch_port", string(portName), "dynamic_addresses")
	if err != nil {
		return nil, err
	}

	dynamicAddressesRaw = strings.TrimSpace(dynamicAddressesRaw)

	// Check if no dynamic IPs set.
	if dynamicAddressesRaw == "[]" {
		return []net.IP{}, nil
	}

	dynamicAddressesRaw, err = unquote(dynamicAddressesRaw)
	if err != nil {
		return nil, fmt.Errorf("Failed unquoting: %w", err)
	}

	dynamicAddresses := strings.Split(strings.TrimSpace(dynamicAddressesRaw), " ")
	dynamicIPs := make([]net.IP, 0, len(dynamicAddresses))

	for _, dynamicAddress := range dynamicAddresses {
		ip := net.ParseIP(dynamicAddress)
		if ip != nil {
			dynamicIPs = append(dynamicIPs, ip)
		}
	}

	return dynamicIPs, nil
}

// LogicalSwitchPortLocationGet returns the last set location of a logical switch port.
func (o *OVN) LogicalSwitchPortLocationGet(portName OVNSwitchPort) (string, error) {
	location, err := o.nbctl("--if-exists", "get", "logical_switch_port", string(portName), "external-ids:"+string(ovnExtIDLXDLocation))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(location), nil
}

// LogicalSwitchPortOptionsSet sets the options for a logical switch port.
func (o *OVN) LogicalSwitchPortOptionsSet(portName OVNSwitchPort, options map[string]string) error {
	args := []string{"lsp-set-options", string(portName)}

	for key, value := range options {
		args = append(args, key+"="+value)
	}

	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalSwitchPortSetDNS sets up the switch port DNS records for the DNS name.
// Returns the DNS record UUID, IPv4 and IPv6 addresses used for DNS records.
func (o *OVN) LogicalSwitchPortSetDNS(switchName OVNSwitch, portName OVNSwitchPort, dnsName string, dnsIPs []net.IP) (OVNDNSUUID, error) {
	// Check if existing DNS record exists for switch port.
	dnsUUID, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--columns=_uuid", "find", "dns",
		fmt.Sprintf("external_ids:%s=%s", ovnExtIDLXDSwitchPort, portName),
	)
	if err != nil {
		return "", err
	}

	cmdArgs := []string{
		fmt.Sprintf("external_ids:%s=%s", ovnExtIDLXDSwitch, switchName),
		fmt.Sprintf("external_ids:%s=%s", ovnExtIDLXDSwitchPort, portName),
	}

	// Only generate DNS records if IPs are supplied.
	if len(dnsIPs) > 0 {
		dnsNameLower := strings.ToLower(dnsName)
		var dnsRecords strings.Builder

		// Generate A and AAAA records.
		dnsRecords.WriteString(`records={"` + dnsNameLower + `"="`)
		for i, dnsIP := range dnsIPs {
			if i > 0 {
				dnsRecords.WriteString(" ")
			}

			dnsRecords.WriteString(dnsIP.String())
		}

		dnsRecords.WriteString(`"`)

		// Generate PTR records.
		for _, dnsIP := range dnsIPs {
			// Trim the "." from the end of the PTR record as OVN doesn't like it.
			dnsRecords.WriteString(` "` + strings.TrimSuffix(dnsutil.Reverse(dnsIP), ".") + `"="` + dnsNameLower + `"`)
		}

		dnsRecords.WriteString("}")

		cmdArgs = append(cmdArgs, dnsRecords.String())
	}

	dnsUUID = strings.TrimSpace(dnsUUID)
	if dnsUUID != "" {
		// Clear any existing DNS name if no IPs supplied.
		if len(dnsIPs) < 1 {
			cmdArgs = append(cmdArgs, "--", "clear", "dns", string(dnsUUID), "records")
		}

		// Update existing record if exists.
		_, err = o.nbctl(append([]string{"set", "dns", dnsUUID}, cmdArgs...)...)
		if err != nil {
			return "", err
		}
	} else {
		// Create new record if needed.
		dnsUUID, err = o.nbctl(append([]string{"create", "dns"}, cmdArgs...)...)
		if err != nil {
			return "", err
		}

		dnsUUID = strings.TrimSpace(dnsUUID)
	}

	// Add DNS record to switch DNS records.
	_, err = o.nbctl("add", "logical_switch", string(switchName), "dns_records", dnsUUID)
	if err != nil {
		return "", err
	}

	return OVNDNSUUID(dnsUUID), nil
}

// LogicalSwitchPortGetDNS returns the logical switch port DNS info (UUID and IPs).
func (o *OVN) LogicalSwitchPortGetDNS(portName OVNSwitchPort) (OVNDNSUUID, []net.IP, error) {
	// Get UUID and DNS IPs for a switch port in the format: "<DNS UUID>,<DNS NAME>=<IP> <IP>"
	output, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--columns=_uuid,records", "find", "dns",
		fmt.Sprintf("external_ids:%s=%s", ovnExtIDLXDSwitchPort, portName),
	)
	if err != nil {
		return "", nil, err
	}

	dnsUUID, recordFields, _ := strings.Cut(strings.TrimSpace(output), ",")

	var ips []net.IP //nolint:prealloc

	// Find IPs in the records fields.
	// These fields can be in several formats, e.g.
	// 1. <name>=<IP>
	// 2. <IP>
	// 3. <reverse IP>.in-addr.arpa=<name>
	// We are only interested in getting the IPs in field formats 1 and 2.
	for recordField := range strings.FieldsSeq(recordFields) {
		a, b, found := strings.Cut(recordField, "=")
		if found {
			a = b // Get IP part of <name>=<IP> type fields.
		}

		// Parse candidate field value to see if its an IP.
		ip := net.ParseIP(a)
		if ip != nil {
			ips = append(ips, ip)
		}
	}

	return OVNDNSUUID(dnsUUID), ips, nil
}

// logicalSwitchPortDeleteDNSAppendArgs adds the command arguments to remove DNS records from a switch port.
// If destroyEntry the DNS entry record itself is also removed, otherwise it is just cleared but left in place.
// Returns args with the commands added to it.
func (o *OVN) logicalSwitchPortDeleteDNSAppendArgs(args []string, switchName OVNSwitch, dnsUUID OVNDNSUUID, destroyEntry bool) []string {
	if len(args) > 0 {
		args = append(args, "--")
	}

	args = append(args, "remove", "logical_switch", string(switchName), "dns_records", string(dnsUUID), "--")

	if destroyEntry {
		args = append(args, "destroy", "dns", string(dnsUUID))
	} else {
		args = append(args, "clear", "dns", string(dnsUUID), "records")
	}

	return args
}

// LogicalSwitchPortDeleteDNS removes DNS records from a switch port.
// If destroyEntry the DNS entry record itself is also removed, otherwise it is just cleared but left in place.
func (o *OVN) LogicalSwitchPortDeleteDNS(switchName OVNSwitch, dnsUUID OVNDNSUUID, destroyEntry bool) error {
	// Remove DNS record association from switch, and remove DNS record entry itself.
	_, err := o.nbctl(o.logicalSwitchPortDeleteDNSAppendArgs(nil, switchName, dnsUUID, destroyEntry)...)
	if err != nil {
		return err
	}

	return nil
}

// logicalSwitchPortDeleteAppendArgs adds the commands to delete the specified logical switch port.
// Returns args with the commands added to it.
func (o *OVN) logicalSwitchPortDeleteAppendArgs(args []string, portName OVNSwitchPort) []string {
	if len(args) > 0 {
		args = append(args, "--")
	}

	args = append(args, "--if-exists", "lsp-del", string(portName))

	return args
}

// LogicalSwitchPortDelete deletes a named logical switch port.
func (o *OVN) LogicalSwitchPortDelete(portName OVNSwitchPort) error {
	_, err := o.nbctl(o.logicalSwitchPortDeleteAppendArgs(nil, portName)...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalSwitchPortCleanup deletes the named logical switch port and its associated config.
func (o *OVN) LogicalSwitchPortCleanup(portName OVNSwitchPort, switchName OVNSwitch, switchPortGroupName OVNPortGroup, dnsUUID OVNDNSUUID) error {
	// Remove any existing rules assigned to the entity.
	removeACLRuleUUIDs, err := o.logicalSwitchPortACLRules(portName)
	if err != nil {
		return err
	}

	args := o.aclRuleDeleteAppendArgs(nil, "port_group", string(switchPortGroupName), removeACLRuleUUIDs)

	// Remove logical switch port.
	args = o.logicalSwitchPortDeleteAppendArgs(args, portName)

	// Remove DNS records.
	if dnsUUID != "" {
		args = o.logicalSwitchPortDeleteDNSAppendArgs(args, switchName, dnsUUID, false)
	}

	_, err = o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalSwitchPortLinkRouter links a logical switch port to a logical router port.
func (o *OVN) LogicalSwitchPortLinkRouter(switchPortName OVNSwitchPort, routerPortName OVNRouterPort) error {
	// Connect logical router port to switch.
	_, err := o.nbctl(
		"lsp-set-type", string(switchPortName), "router", "--",
		"lsp-set-addresses", string(switchPortName), "router", "--",
		"lsp-set-options", string(switchPortName), "nat-addresses=router", "router-port="+string(routerPortName),
	)
	if err != nil {
		return err
	}

	return nil
}

// LogicalSwitchPortLinkProviderNetwork links a logical switch port to a provider network.
func (o *OVN) LogicalSwitchPortLinkProviderNetwork(switchPortName OVNSwitchPort, extNetworkName string) error {
	// Forward any unknown MAC frames down this port.
	_, err := o.nbctl(
		"lsp-set-addresses", string(switchPortName), "unknown", "--",
		"lsp-set-type", string(switchPortName), "localnet", "--",
		"lsp-set-options", string(switchPortName), "network_name="+extNetworkName,
	)
	if err != nil {
		return err
	}

	return nil
}

// ChassisGroupAdd adds a new HA chassis group.
// If mayExist is true, then an existing resource of the same name is not treated as an error.
func (o *OVN) ChassisGroupAdd(haChassisGroupName OVNChassisGroup, mayExist bool) error {
	if mayExist {
		// Check if it exists (sadly ha-chassis-group-add doesn't provide --may-exist option).
		_, err := o.nbctl("list", "HA_Chassis_Group", string(haChassisGroupName))
		if err == nil {
			return nil // Chassis group exists.
		}
	}

	_, err := o.nbctl("ha-chassis-group-add", string(haChassisGroupName))
	if err != nil {
		return err
	}

	return nil
}

// ChassisGroupDelete deletes an HA chassis group.
func (o *OVN) ChassisGroupDelete(haChassisGroupName OVNChassisGroup) error {
	// ovn-nbctl doesn't provide an "--if-exists" option for removing chassis groups.
	existing, err := o.nbctl("--no-headings", "--data=bare", "--columns=name", "find", "ha_chassis_group", "name="+string(haChassisGroupName))
	if err != nil {
		return err
	}

	// Remove chassis group if exists.
	if strings.TrimSpace(existing) != "" {
		_, err := o.nbctl("ha-chassis-group-del", string(haChassisGroupName))
		if err != nil {
			return err
		}
	}

	return nil
}

// ChassisGroupChassisAdd adds a chassis ID to an HA chassis group with the specified priority.
func (o *OVN) ChassisGroupChassisAdd(haChassisGroupName OVNChassisGroup, chassisID string, priority uint) error {
	_, err := o.nbctl("ha-chassis-group-add-chassis", string(haChassisGroupName), chassisID, strconv.FormatUint(uint64(priority), 10))
	if err != nil {
		return err
	}

	return nil
}

// ChassisGroupChassisDelete deletes a chassis ID from an HA chassis group.
func (o *OVN) ChassisGroupChassisDelete(haChassisGroupName OVNChassisGroup, chassisID string) error {
	// Map UUIDs with chassis_names.
	output, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--columns=_uuid,chassis_name", "find", "ha_chassis")
	if err != nil {
		return err
	}

	lines := shared.SplitNTrimSpace(output, "\n", -1, true)

	uuidToChassis := make(map[string]string, len(lines))

	for _, line := range lines {
		// a74125a8-b580-4763-b389-11ce2c8c5509,node2
		key, value, match := strings.Cut(line, ",")
		if match {
			uuidToChassis[key] = value
		}
	}

	// Check if chassis group exists. ovn-nbctl doesn't provide an "--if-exists" option for this.
	output, err = o.nbctl("--no-headings", "--data=bare", "--columns=name,ha_chassis", "find", "ha_chassis_group", "name="+string(haChassisGroupName))
	if err != nil {
		return err
	}

	lines = shared.SplitNTrimSpace(output, "\n", -1, true)
	if len(lines) > 1 {
		existingChassisGroup := lines[0]
		members := shared.SplitNTrimSpace(lines[1], " ", -1, true)

		// Remove chassis from group if exists.
		if existingChassisGroup == string(haChassisGroupName) {
			for _, member := range members {
				name, found := uuidToChassis[member]
				if found && name == chassisID {
					_, err := o.nbctl("ha-chassis-group-remove-chassis", string(haChassisGroupName), chassisID)
					if err != nil {
						return err
					}

					break
				}
			}
		}
	}

	// Nothing to do if chassis group doesn't exist.
	return nil
}

// PortGroupInfo returns the port group UUID or empty string if port doesn't exist, and whether the port group has
// any ACL rules defined on it.
func (o *OVN) PortGroupInfo(portGroupName OVNPortGroup) (OVNPortGroupUUID, bool, error) {
	groupInfo, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--columns=_uuid,name,acl", "find", "port_group",
		"name="+string(portGroupName),
	)
	if err != nil {
		return "", false, err
	}

	groupParts := shared.SplitNTrimSpace(groupInfo, ",", 3, true)
	if len(groupParts) == 3 {
		if groupParts[1] == string(portGroupName) {
			aclParts := shared.SplitNTrimSpace(groupParts[2], ",", -1, true)

			return OVNPortGroupUUID(groupParts[0]), len(aclParts) > 0, nil
		}
	}

	return "", false, nil
}

// PortGroupAdd creates a new port group and optionally adds logical switch ports to the group.
func (o *OVN) PortGroupAdd(projectID int64, portGroupName OVNPortGroup, associatedPortGroup OVNPortGroup, associatedSwitch OVNSwitch, initialPortMembers ...OVNSwitchPort) error {
	args := []string{"pg-add", string(portGroupName)}
	for _, portName := range initialPortMembers {
		args = append(args, string(portName))
	}

	args = append(args, "--", "set", "port_group", string(portGroupName),
		fmt.Sprintf("external_ids:%s=%d", ovnExtIDLXDProjectID, projectID),
	)

	if associatedPortGroup != "" || associatedSwitch != "" {
		if associatedPortGroup != "" {
			args = append(args, fmt.Sprintf("external_ids:%s=%s", ovnExtIDLXDPortGroup, associatedPortGroup))
		}

		if associatedSwitch != "" {
			args = append(args, fmt.Sprintf("external_ids:%s=%s", ovnExtIDLXDSwitch, associatedSwitch))
		}
	}

	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// PortGroupDelete deletes port groups along with their ACL rules.
func (o *OVN) PortGroupDelete(portGroupNames ...OVNPortGroup) error {
	args := make([]string, 0, 5*len(portGroupNames))

	for _, portGroupName := range portGroupNames {
		if len(args) > 0 {
			args = append(args, "--")
		}

		args = append(args, "--if-exists", "destroy", "port_group", string(portGroupName))
	}

	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// PortGroupListByProject finds the port groups that are associated to the project ID.
func (o *OVN) PortGroupListByProject(projectID int64) ([]OVNPortGroup, error) {
	output, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--columns=name", "find", "port_group",
		fmt.Sprintf("external_ids:%s=%d", ovnExtIDLXDProjectID, projectID),
	)
	if err != nil {
		return nil, err
	}

	lines := shared.SplitNTrimSpace(strings.TrimSpace(output), "\n", -1, true)
	portGroups := make([]OVNPortGroup, 0, len(lines))

	for _, line := range lines {
		portGroups = append(portGroups, OVNPortGroup(line))
	}

	return portGroups, nil
}

// PortGroupMemberChange adds/removes logical switch ports (by UUID) to/from existing port groups.
func (o *OVN) PortGroupMemberChange(addMembers map[OVNPortGroup][]OVNSwitchPortUUID, removeMembers map[OVNPortGroup][]OVNSwitchPortUUID) error {
	args := []string{}

	for portGroupName, portMemberUUIDs := range addMembers {
		for _, portMemberUUID := range portMemberUUIDs {
			if len(args) > 0 {
				args = append(args, "--")
			}

			args = append(args, "add", "port_group", string(portGroupName), "ports", string(portMemberUUID))
		}
	}

	for portGroupName, portMemberUUIDs := range removeMembers {
		for _, portMemberUUID := range portMemberUUIDs {
			if len(args) > 0 {
				args = append(args, "--")
			}

			args = append(args, "--if-exists", "remove", "port_group", string(portGroupName), "ports", string(portMemberUUID))
		}
	}

	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// PortGroupSetACLRules applies a set of rules to the specified port group. Any existing rules are removed.
func (o *OVN) PortGroupSetACLRules(portGroupName OVNPortGroup, matchReplace map[string]string, aclRules ...OVNACLRule) error {
	// Remove any existing rules assigned to the entity.
	args := []string{"clear", "port_group", string(portGroupName), "acls"}

	// Add new rules.
	externalIDs := map[string]string{
		ovnExtIDLXDPortGroup: string(portGroupName),
	}

	args = o.aclRuleAddAppendArgs(args, "port_group", string(portGroupName), externalIDs, matchReplace, aclRules...)

	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// aclRuleAddAppendArgs adds the commands to args that add the provided ACL rules to the specified OVN entity.
// Returns args with the ACL rule add commands added to it.
func (o *OVN) aclRuleAddAppendArgs(args []string, entityTable string, entityName string, externalIDs map[string]string, matchReplace map[string]string, aclRules ...OVNACLRule) []string {
	for i, rule := range aclRules {
		if len(args) > 0 {
			args = append(args, "--")
		}

		// Perform any replacements requested on the Match string.
		for find, replace := range matchReplace {
			rule.Match = strings.ReplaceAll(rule.Match, find, replace)
		}

		// Add command to create ACL rule.
		args = append(args, "--id=@id"+strconv.Itoa(i), "create", "acl",
			"action="+rule.Action,
			"direction="+rule.Direction,
			"priority="+strconv.Itoa(rule.Priority),
			"match="+strconv.Quote(rule.Match),
		)

		if rule.Log {
			args = append(args, "log=true")

			if rule.LogName != "" {
				args = append(args, "name="+rule.LogName)
			}
		}

		for k, v := range externalIDs {
			args = append(args, "external_ids:"+k+"="+v)
		}

		// Add command to assign ACL rule to entity.
		args = append(args, "--", "add", entityTable, entityName, "acl", "@id"+strconv.Itoa(i))
	}

	return args
}

// aclRuleDeleteAppendArgs adds the commands to args that delete the provided ACL rules from the specified OVN entity.
// Returns args with the ACL rule delete commands added to it.
func (o *OVN) aclRuleDeleteAppendArgs(args []string, entityTable string, entityName string, aclRuleUUIDs []string) []string {
	for _, aclRuleUUID := range aclRuleUUIDs {
		if len(args) > 0 {
			args = append(args, "--")
		}

		args = append(args, "remove", entityTable, string(entityName), "acl", aclRuleUUID)
	}

	return args
}

// PortGroupPortSetACLRules applies a set of rules for the logical switch port in the specified port group.
// Any existing rules for that logical switch port in the port group are removed.
func (o *OVN) PortGroupPortSetACLRules(portGroupName OVNPortGroup, portName OVNSwitchPort, aclRules ...OVNACLRule) error {
	// Remove any existing rules assigned to the entity.
	removeACLRuleUUIDs, err := o.logicalSwitchPortACLRules(portName)
	if err != nil {
		return err
	}

	args := o.aclRuleDeleteAppendArgs(nil, "port_group", string(portGroupName), removeACLRuleUUIDs)

	// Add new rules.
	externalIDs := map[string]string{
		ovnExtIDLXDPortGroup:  string(portGroupName),
		ovnExtIDLXDSwitchPort: string(portName),
	}

	args = o.aclRuleAddAppendArgs(args, "port_group", string(portGroupName), externalIDs, nil, aclRules...)

	_, err = o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// PortGroupPortClearACLRules clears any rules assigned to the logical switch port in the specified port group.
func (o *OVN) PortGroupPortClearACLRules(portGroupName OVNPortGroup, portName OVNSwitchPort) error {
	// Remove any existing rules assigned to the entity.
	removeACLRuleUUIDs, err := o.logicalSwitchPortACLRules(portName)
	if err != nil {
		return err
	}

	args := o.aclRuleDeleteAppendArgs(nil, "port_group", string(portGroupName), removeACLRuleUUIDs)

	if len(args) > 0 {
		_, err = o.nbctl(args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// loadBalancerUUIDs returns list of UUID records for named load balancer.
func (o *OVN) loadBalancerUUIDs(loadBalancerName OVNLoadBalancer) ([]string, error) {
	lbTCPName := string(loadBalancerName) + "-tcp"
	lbUDPName := string(loadBalancerName) + "-udp"

	var lbUUIDs []string //nolint:prealloc

	// Use find command in order to workaround OVN bug where duplicate records of same name can exist.
	for _, lbName := range []string{lbTCPName, lbUDPName} {
		output, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--columns=_uuid", "find", "load_balancer", `name="`+lbName+`"`)
		if err != nil {
			return nil, err
		}

		lbUUIDs = append(lbUUIDs, shared.SplitNTrimSpace(strings.TrimSpace(output), "\n", -1, true)...)
	}

	return lbUUIDs, nil
}

// LoadBalancerApply creates a new load balancer (if doesn't exist) on the specified routers and switches.
// Providing an empty set of vips will delete the load balancer.
func (o *OVN) LoadBalancerApply(loadBalancerName OVNLoadBalancer, routers []OVNRouter, switches []OVNSwitch, vips ...OVNLoadBalancerVIP) error {
	lbTCPName := string(loadBalancerName) + "-tcp"
	lbUDPName := string(loadBalancerName) + "-udp"

	// Remove load balancers if they exist.
	lbUUIDs, err := o.loadBalancerUUIDs(loadBalancerName)
	if err != nil {
		return fmt.Errorf("Failed getting UUIDs: %w", err)
	}

	args := make([]string, 0, 5*len(lbUUIDs))

	for _, lbUUID := range lbUUIDs {
		if len(args) > 0 {
			args = append(args, "--")
		}

		args = append(args, "--if-exists", "destroy", "load_balancer", lbUUID)
	}

	// ipToString wraps IPv6 addresses in square brackets.
	ipToString := func(ip net.IP) string {
		if ip.To4() == nil {
			return "[" + ip.String() + "]"
		}

		return ip.String()
	}

	// Build up the commands to add VIPs to the load balancer.
	for _, r := range vips {
		if r.ListenAddress == nil {
			return errors.New("Missing VIP listen address")
		}

		if len(r.Targets) == 0 {
			return errors.New("Missing VIP target(s)")
		}

		if len(args) > 0 {
			args = append(args, "--")
		}

		if r.Protocol == "udp" {
			args = append(args, "lb-add", lbUDPName)
		} else {
			args = append(args, "lb-add", lbTCPName)
		}

		targetArgs := make([]string, 0, len(r.Targets))

		for _, target := range r.Targets {
			if (r.ListenPort > 0 && target.Port <= 0) || (target.Port > 0 && r.ListenPort <= 0) {
				return errors.New("The listen and target ports must be specified together")
			}

			if r.ListenPort > 0 {
				targetArgs = append(targetArgs, ipToString(target.Address)+":"+strconv.FormatUint(target.Port, 10))
			} else {
				targetArgs = append(targetArgs, ipToString(target.Address))
			}
		}

		if r.ListenPort > 0 {
			args = append(args,
				ipToString(r.ListenAddress)+":"+strconv.FormatUint(r.ListenPort, 10),
				strings.Join(targetArgs, ","),
				r.Protocol,
			)
		} else {
			args = append(args,
				ipToString(r.ListenAddress),
				strings.Join(targetArgs, ","),
			)
		}
	}

	// Apply the load balancer changes.
	if len(args) > 0 {
		_, err := o.nbctl(args...)
		if err != nil {
			return err
		}
	}

	// If there are some VIP rules then associate the load balancer to the requested routers and switches.
	if len(vips) > 0 {
		args := make([]string, 0, 6*len(lbUUIDs))

		// Get fresh list of load balancer UUIDs.
		lbUUIDs, err := o.loadBalancerUUIDs(loadBalancerName)
		if err != nil {
			return fmt.Errorf("Failed getting UUIDs: %w", err)
		}

		for _, lbUUID := range lbUUIDs {
			if len(args) > 0 {
				args = append(args, "--")
			}

			for _, r := range routers {
				args = append(args, "add", "logical_router", string(r), "load_balancer", lbUUID)
			}
		}

		if len(args) > 0 {
			_, err = o.nbctl(args...)
			if err != nil {
				return err
			}
		}

		for _, lbUUID := range lbUUIDs {
			for _, s := range switches {
				_, err = o.nbctl("ls-lb-add", string(s), lbUUID)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// LoadBalancerDelete deletes the specified load balancer(s).
func (o *OVN) LoadBalancerDelete(loadBalancerNames ...OVNLoadBalancer) error {
	args := make([]string, 0, 5*len(loadBalancerNames))

	for _, loadBalancerName := range loadBalancerNames {
		lbUUIDs, err := o.loadBalancerUUIDs(loadBalancerName)
		if err != nil {
			return fmt.Errorf("Failed getting UUIDs: %w", err)
		}

		// Remove load balancers if they exist.
		for _, lbUUID := range lbUUIDs {
			if len(args) > 0 {
				args = append(args, "--")
			}

			args = append(args, "--if-exists", "destroy", "load_balancer", lbUUID)
		}
	}

	if len(args) > 0 {
		_, err := o.nbctl(args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// AddressSetCreate creates address sets for IP versions 4 and 6 in the format "<addressSetPrefix>_ip<IP version>".
// Populates them with the relevant addresses supplied.
func (o *OVN) AddressSetCreate(addressSetPrefix OVNAddressSet, addresses ...net.IPNet) error {
	args := []string{
		"create", "address_set", fmt.Sprintf("name=%s_ip%d", addressSetPrefix, 4),
		"--", "create", "address_set", fmt.Sprintf("name=%s_ip%d", addressSetPrefix, 6),
	}

	for _, address := range addresses {
		if len(args) > 0 {
			args = append(args, "--")
		}

		var ipVersion uint = 4
		if address.IP.To4() == nil {
			ipVersion = 6
		}

		args = append(args, "add", "address_set", fmt.Sprintf("%s_ip%d", addressSetPrefix, ipVersion), "addresses", fmt.Sprintf(`"%s"`, address.String()))
	}

	if len(args) > 0 {
		_, err := o.nbctl(args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// AddressSetAdd adds the supplied addresses to the address sets, or creates a new address sets if needed.
// The address set name used is "<addressSetPrefix>_ip<IP version>", e.g. "foo_ip4".
func (o *OVN) AddressSetAdd(addressSetPrefix OVNAddressSet, addresses ...net.IPNet) error {
	args := make([]string, 0, 6*len(addresses))

	ipVersions := make(map[uint]struct{})

	for _, address := range addresses {
		if len(args) > 0 {
			args = append(args, "--")
		}

		var ipVersion uint = 4
		if address.IP.To4() == nil {
			ipVersion = 6
		}

		// Track IP versions seen so we can create address sets if needed.
		ipVersions[ipVersion] = struct{}{}

		args = append(args, "add", "address_set", fmt.Sprintf("%s_ip%d", addressSetPrefix, ipVersion), "addresses", fmt.Sprintf(`"%s"`, address.String()))
	}

	if len(args) > 0 {
		// Optimistically assume all required address sets exist (they normally will).
		_, err := o.nbctl(args...)
		if err != nil {
			// Try creating the address sets one at a time, but ignore errors here in case some of the
			// address sets already exist. If there was a problem creating the address set it will be
			// revealead when we run the original command again next.
			for ipVersion := range ipVersions {
				_, _ = o.nbctl("create", "address_set", fmt.Sprintf("name=%s_ip%d", addressSetPrefix, ipVersion))
			}

			// Try original command again.
			_, err := o.nbctl(args...)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// AddressSetRemove removes the supplied addresses from the address set.
// The address set name used is "<addressSetPrefix>_ip<IP version>", e.g. "foo_ip4".
func (o *OVN) AddressSetRemove(addressSetPrefix OVNAddressSet, addresses ...net.IPNet) error {
	args := make([]string, 0, 7*len(addresses))

	for _, address := range addresses {
		if len(args) > 0 {
			args = append(args, "--")
		}

		var ipVersion uint = 4
		if address.IP.To4() == nil {
			ipVersion = 6
		}

		args = append(args, "--if-exists", "remove", "address_set", fmt.Sprintf("%s_ip%d", addressSetPrefix, ipVersion), "addresses", fmt.Sprintf(`"%s"`, address.String()))
	}

	if len(args) > 0 {
		_, err := o.nbctl(args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// AddressSetDelete deletes address sets for IP versions 4 and 6 in the format "<addressSetPrefix>_ip<IP version>".
func (o *OVN) AddressSetDelete(addressSetPrefix OVNAddressSet) error {
	_, err := o.nbctl(
		"--if-exists", "destroy", "address_set", fmt.Sprintf("%s_ip%d", addressSetPrefix, 4),
		"--", "--if-exists", "destroy", "address_set", fmt.Sprintf("%s_ip%d", addressSetPrefix, 6),
	)
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterPolicyApply removes any existing policies and applies the new policies to the specified router.
func (o *OVN) LogicalRouterPolicyApply(routerName OVNRouter, policies ...OVNRouterPolicy) error {
	args := []string{"lr-policy-del", string(routerName)}

	for _, policy := range policies {
		args = append(args, "--", "lr-policy-add", string(routerName), strconv.Itoa(policy.Priority), policy.Match, policy.Action)
	}

	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterRoutes returns a list of static routes in the main route table of the logical router.
func (o *OVN) LogicalRouterRoutes(routerName OVNRouter) ([]OVNRouterRoute, error) {
	output, err := o.nbctl("lr-route-list", string(routerName))
	if err != nil {
		return nil, err
	}

	lines := shared.SplitNTrimSpace(strings.TrimSpace(output), "\n", -1, true)
	routes := make([]OVNRouterRoute, 0, len(lines))

	mainTable := true // Assume output starts with main table (supports ovn versions without multiple tables).
	for i, line := range lines {
		if line == "IPv4 Routes" || line == "IPv6 Routes" {
			continue // Ignore heading category lines.
		}

		// Keep track of which route table we are looking at.
		if strings.HasPrefix(line, "Route Table") {
			if line == "Route Table <main>:" {
				mainTable = true
			} else {
				mainTable = false
			}

			continue
		}

		if !mainTable {
			continue // We don't currently consider routes in other route tables.
		}

		// E.g. "10.97.31.0/24 10.97.31.1 dst-ip [optional-some-router-port-name]"
		fields := strings.Fields(line)
		fieldsLen := len(fields)

		if fieldsLen <= 0 {
			continue // Ignore empty lines.
		} else if fieldsLen < 3 || fieldsLen > 4 {
			return nil, fmt.Errorf("Unrecognised static route item output on line %d: %q", i, line)
		}

		var route OVNRouterRoute

		// ovn-nbctl doesn't output single-host route prefixes in CIDR format, so do the conversion here.
		ip := net.ParseIP(fields[0])
		if ip != nil {
			if ip.To4() == nil {
				fields[0] = ip.String() + "/128"
			} else {
				fields[0] = ip.String() + "/32"
			}
		}

		_, prefix, err := net.ParseCIDR(fields[0])
		if err != nil {
			return nil, fmt.Errorf("Invalid static route prefix on line %d: %q", i, fields[0])
		}

		route.Prefix = *prefix
		route.NextHop = net.ParseIP(fields[1])

		if fieldsLen > 3 {
			route.Port = OVNRouterPort(fields[3])
		}

		routes = append(routes, route)
	}

	return routes, nil
}

// LogicalRouterPeeringApply applies a peering relationship between two logical routers.
func (o *OVN) LogicalRouterPeeringApply(opts OVNRouterPeering) error {
	if len(opts.LocalRouterPortIPs) <= 0 || len(opts.TargetRouterPortIPs) <= 0 {
		return errors.New("IPs not populated for both router ports")
	}

	// Remove peering router ports and static routes using ports from both routers.
	// Run the delete step as a separate command to workaround a bug in OVN.
	err := o.LogicalRouterPeeringDelete(opts)
	if err != nil {
		return err
	}

	// Start fresh command set.
	var args []string //nolint:prealloc

	// Will use the first IP from each family of the router port interfaces.
	localRouterGatewayIPs := make(map[uint]net.IP, 0)
	targetRouterGatewayIPs := make(map[uint]net.IP, 0)

	// Setup local router port peered with target router port.
	args = append(args, "--", "lrp-add", string(opts.LocalRouter), string(opts.LocalRouterPort), opts.LocalRouterPortMAC.String())
	for _, ipNet := range opts.LocalRouterPortIPs {
		ipVersion := uint(4)
		if ipNet.IP.To4() == nil {
			ipVersion = 6
		}

		if localRouterGatewayIPs[ipVersion] == nil {
			localRouterGatewayIPs[ipVersion] = ipNet.IP
		}

		args = append(args, ipNet.String())
	}

	args = append(args, "peer="+string(opts.TargetRouterPort))

	// Setup target router port peered with local router port.
	args = append(args, "--", "lrp-add", string(opts.TargetRouter), string(opts.TargetRouterPort), opts.TargetRouterPortMAC.String())
	for _, ipNet := range opts.TargetRouterPortIPs {
		ipVersion := uint(4)
		if ipNet.IP.To4() == nil {
			ipVersion = 6
		}

		if targetRouterGatewayIPs[ipVersion] == nil {
			targetRouterGatewayIPs[ipVersion] = ipNet.IP
		}

		args = append(args, ipNet.String())
	}

	args = append(args, "peer="+string(opts.LocalRouterPort))

	// Add routes using the first router gateway IP for each family for next hop address.
	for _, route := range opts.LocalRouterRoutes {
		ipVersion := uint(4)
		if route.IP.To4() == nil {
			ipVersion = 6
		}

		nextHopIP := targetRouterGatewayIPs[ipVersion]

		if nextHopIP == nil {
			return fmt.Errorf("Missing target router port IPv%d address for local route %q nexthop address", ipVersion, route.String())
		}

		args = append(args, "--", "--may-exist", "lr-route-add", string(opts.LocalRouter), route.String(), nextHopIP.String(), string(opts.LocalRouterPort))
	}

	for _, route := range opts.TargetRouterRoutes {
		ipVersion := uint(4)
		if route.IP.To4() == nil {
			ipVersion = 6
		}

		nextHopIP := localRouterGatewayIPs[ipVersion]

		if nextHopIP == nil {
			return fmt.Errorf("Missing local router port IPv%d address for target route %q nexthop address", ipVersion, route.String())
		}

		args = append(args, "--", "--may-exist", "lr-route-add", string(opts.TargetRouter), route.String(), nextHopIP.String(), string(opts.TargetRouterPort))
	}

	if len(args) > 0 {
		_, err := o.nbctl(args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// LogicalRouterPeeringDelete deletes a peering relationship between two logical routers.
// Requires LocalRouter, LocalRouterPort, TargetRouter and TargetRouterPort opts fields to be populated.
func (o *OVN) LogicalRouterPeeringDelete(opts OVNRouterPeering) error {
	// Remove peering router ports and static routes using ports from both routers.
	if opts.LocalRouter == "" || opts.TargetRouter == "" {
		return errors.New("Router names not populated for both routers")
	}

	args := []string{
		"--if-exists", "lrp-del", string(opts.LocalRouterPort), "--",
		"--if-exists", "lrp-del", string(opts.TargetRouterPort),
	}

	// Remove static routes from both routers that use the respective peering router ports.
	staticRoutes, err := o.LogicalRouterRoutes(opts.LocalRouter)
	if err != nil {
		return fmt.Errorf("Failed getting static routes for local peer router %q: %w", opts.LocalRouter, err)
	}

	for _, staticRoute := range staticRoutes {
		if staticRoute.Port == opts.LocalRouterPort {
			args = append(args, "--", "lr-route-del", string(opts.LocalRouter), staticRoute.Prefix.String(), staticRoute.NextHop.String(), string(opts.LocalRouterPort))
		}
	}

	staticRoutes, err = o.LogicalRouterRoutes(opts.TargetRouter)
	if err != nil {
		return fmt.Errorf("Failed getting static routes for target peer router %q: %w", opts.TargetRouter, err)
	}

	for _, staticRoute := range staticRoutes {
		if staticRoute.Port == opts.TargetRouterPort {
			args = append(args, "--", "lr-route-del", string(opts.TargetRouter), staticRoute.Prefix.String(), staticRoute.NextHop.String(), string(opts.TargetRouterPort))
		}
	}

	if len(args) > 0 {
		_, err := o.nbctl(args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetHardwareAddress gets the hardware address of the logical router port.
func (o *OVN) GetHardwareAddress(ovnRouterPort OVNRouterPort) (string, error) {
	nameFilter := "name=" + string(ovnRouterPort)
	hwaddr, err := o.nbctl("--no-headings", "--data=bare", "--format=csv", "--columns=mac", "find", "Logical_Router_Port", nameFilter)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(hwaddr), nil
}

// GetLogicalRouterPortActiveChassisHostname gets the hostname of the chassis managing the logical router port.
func (o *OVN) GetLogicalRouterPortActiveChassisHostname(ovnRouterPort OVNRouterPort) (string, error) {
	// Get the chassis ID from port bindings where the logical port is a chassis redirect (prepended "cr-") of the logical router port name.
	filter := "logical_port=cr-" + string(ovnRouterPort)
	chassisID, err := o.sbctl("--no-headings", "--columns=chassis", "--data=bare", "--format=csv", "find", "Port_Binding", filter)
	if err != nil {
		return "", err
	}

	if chassisID == "" {
		return "", errors.New("No chassis found")
	}

	hostname, err := o.sbctl("get", "Chassis", strings.TrimSpace(chassisID), "hostname")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(hostname), err
}
