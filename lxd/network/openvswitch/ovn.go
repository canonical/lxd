package openvswitch

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/lxc/lxd/shared"
)

// OVNRouter OVN router name.
type OVNRouter string

// OVNRouterPort OVN router port name.
type OVNRouterPort string

// OVNSwitch OVN switch name.
type OVNSwitch string

// OVNSwitchPort OVN switch port name.
type OVNSwitchPort string

// OVNChassisGroup OVN HA chassis group name.
type OVNChassisGroup string

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

// ErrOVNNoPortIPs used when no IPs are found for a logical port.
var ErrOVNNoPortIPs = fmt.Errorf("No port IPs")

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
	UUID string
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
}

// OVNDHCPv6Opts IPv6 DHCP option set that can be created (and then applied to a switch port by resulting ID).
type OVNDHCPv6Opts struct {
	ServerID           net.HardwareAddr
	RecursiveDNSServer []net.IP
	DNSSearchList      []string
}

// OVNSwitchPortOpts options that can be applied to a swich port.
type OVNSwitchPortOpts struct {
	MAC          net.HardwareAddr // Optional, if nil will be set to dynamic.
	IPs          []net.IP         // Optional, if empty IPs will be set to dynamic.
	DHCPv4OptsID string           // Optional, if empty, no DHCPv4 enabled on port.
	DHCPv6OptsID string           // Optional, if empty, no DHCPv6 enabled on port.
}

// NewOVN initialises new OVN wrapper.
func NewOVN() *OVN {
	return &OVN{}
}

// OVN command wrapper.
type OVN struct {
	dbAddr string
}

// SetDatabaseAddress sets the address that runs the OVN northbound and southbound databases.
func (o *OVN) SetDatabaseAddress(addr string) {
	o.dbAddr = addr
}

// getNorthboundDB returns connection string to use for northbound database.
func (o *OVN) getNorthboundDB() string {
	if o.dbAddr == "" {
		return "unix:/var/run/ovn/ovnnb_db.sock"
	}

	return o.dbAddr
}

// nbctl executes ovn-nbctl with arguments to connect to wrapper's northbound database.
func (o *OVN) nbctl(args ...string) (string, error) {
	dbAddr := o.getNorthboundDB()
	if strings.HasPrefix(dbAddr, "unix:") {
		dbAddr = fmt.Sprintf("unix:%s", shared.HostPathFollow(strings.TrimPrefix(dbAddr, "unix:")))
	}

	return shared.RunCommand("ovn-nbctl", append([]string{"--db", dbAddr}, args...)...)
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
		args = append(args, "--may-exist")
	}

	_, err := o.nbctl(append(args, "lr-nat-add", string(routerName), "snat", extIP.String(), intNet.String())...)
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

// LogicalRouterDNATSNATAdd adds a DNAT and SNAT rule to a logical router to translate packets from extIP to intIP.
func (o *OVN) LogicalRouterDNATSNATAdd(routerName OVNRouter, extIP net.IP, intIP net.IP, stateless bool, mayExist bool) error {
	args := []string{}

	if mayExist {
		args = append(args, "--may-exist")
	}

	if stateless {
		args = append(args, "--stateless")
	}

	_, err := o.nbctl(append(args, "lr-nat-add", string(routerName), "dnat_and_snat", extIP.String(), intIP.String())...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterDNATSNATDelete deletes a DNAT and SNAT rule from a logical router.
func (o *OVN) LogicalRouterDNATSNATDelete(routerName OVNRouter, extIP net.IP) error {
	_, err := o.nbctl("--if-exists", "lr-nat-del", string(routerName), "dnat_and_snat", extIP.String())
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterRouteAdd adds a static route to the logical router.
func (o *OVN) LogicalRouterRouteAdd(routerName OVNRouter, destination *net.IPNet, nextHop net.IP, mayExist bool) error {
	args := []string{}

	if mayExist {
		args = append(args, "--may-exist")
	}

	_, err := o.nbctl(append(args, "lr-route-add", string(routerName), destination.String(), nextHop.String())...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterRouteDelete deletes a static route from the logical router.
// If nextHop is specified as nil, then any route matching the destination is removed.
func (o *OVN) LogicalRouterRouteDelete(routerName OVNRouter, destination *net.IPNet, nextHop net.IP) error {
	args := []string{"--if-exists", "lr-route-del", string(routerName), destination.String()}

	if nextHop != nil {
		args = append(args, nextHop.String())
	}

	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterPortAdd adds a named logical router port to a logical router.
func (o *OVN) LogicalRouterPortAdd(routerName OVNRouter, portName OVNRouterPort, mac net.HardwareAddr, ipAddr []*net.IPNet, mayExist bool) error {
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
				fmt.Sprintf(`networks="%s"`, strings.Join(ips, `","`)),
				fmt.Sprintf(`mac="%s"`, fmt.Sprintf(mac.String())),
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
		fmt.Sprintf("ipv6_ra_configs:send_periodic=%t", opts.SendPeriodic),
		fmt.Sprintf("ipv6_ra_configs:address_mode=%s", string(opts.AddressMode)),
	}

	if opts.MaxInterval > 0 {
		args = append(args, fmt.Sprintf("ipv6_ra_configs:max_interval=%d", opts.MaxInterval/time.Second))
	}

	if opts.MinInterval > 0 {
		args = append(args, fmt.Sprintf("ipv6_ra_configs:min_interval=%d", opts.MinInterval/time.Second))
	}

	if opts.MTU > 0 {
		args = append(args, fmt.Sprintf("ipv6_ra_configs:mtu=%d", opts.MTU))
	}

	if len(opts.DNSSearchList) > 0 {
		args = append(args, fmt.Sprintf("ipv6_ra_configs:dnssl=%s", strings.Join(opts.DNSSearchList, ",")))
	}

	if opts.RecursiveDNSServer != nil {
		args = append(args, fmt.Sprintf("ipv6_ra_configs:rdnss=%s", opts.RecursiveDNSServer.String()))
	}

	// Configure IPv6 Router Advertisements.
	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalRouterPortLinkChassisGroup links a logical router port to a HA chassis group.
func (o *OVN) LogicalRouterPortLinkChassisGroup(portName OVNRouterPort, haChassisGroupName OVNChassisGroup) error {
	chassisGroupID, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--colum=_uuid", "find", "ha_chassis_group", fmt.Sprintf("name=%s", haChassisGroupName))
	if err != nil {
		return err
	}

	chassisGroupID = strings.TrimSpace(chassisGroupID)

	if chassisGroupID == "" {
		return fmt.Errorf("Chassis group not found")
	}

	_, err = o.nbctl("set", "logical_router_port", string(portName), fmt.Sprintf("ha_chassis_group=%s", chassisGroupID))
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
	_, err := o.nbctl("--if-exists", "ls-del", string(switchName))
	if err != nil {
		return err
	}

	err = o.LogicalSwitchDHCPOptionsDelete(switchName)
	if err != nil {
		return err
	}

	err = o.logicalSwitchDNSRecordsDelete(switchName)
	if err != nil {
		return err
	}

	return nil
}

// LogicalSwitchSetIPAllocation sets the IP allocation config on the logical switch.
func (o *OVN) LogicalSwitchSetIPAllocation(switchName OVNSwitch, opts *OVNIPAllocationOpts) error {
	var removeOtherConfigKeys []string
	args := []string{"set", "logical_switch", string(switchName)}

	if opts.PrefixIPv4 != nil {
		args = append(args, fmt.Sprintf("other_config:subnet=%s", opts.PrefixIPv4.String()))
	} else {
		removeOtherConfigKeys = append(removeOtherConfigKeys, "subnet")
	}

	if opts.PrefixIPv6 != nil {
		args = append(args, fmt.Sprintf("other_config:ipv6_prefix=%s", opts.PrefixIPv6.String()))
	} else {
		removeOtherConfigKeys = append(removeOtherConfigKeys, "ipv6_prefix")
	}

	if len(opts.ExcludeIPv4) > 0 {
		excludeIPs := make([]string, 0, len(opts.ExcludeIPv4))
		for _, v := range opts.ExcludeIPv4 {
			if v.Start == nil {
				return fmt.Errorf("Invalid exclude IPv4 range start address")
			} else if v.End == nil {
				excludeIPs = append(excludeIPs, v.Start.String())
			} else {
				excludeIPs = append(excludeIPs, fmt.Sprintf("%s..%s", v.Start.String(), v.End.String()))
			}
		}

		args = append(args, fmt.Sprintf("other_config:exclude_ips=%s", strings.Join(excludeIPs, " ")))
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
func (o *OVN) LogicalSwitchDHCPv4OptionsSet(switchName OVNSwitch, uuid string, subnet *net.IPNet, opts *OVNDHCPv4Opts) error {
	var err error

	if uuid != "" {
		_, err = o.nbctl("set", "dhcp_option", uuid,
			fmt.Sprintf("external_ids:lxd_switch=%s", string(switchName)),
			fmt.Sprintf("cidr=%s", subnet.String()),
		)
		if err != nil {
			return err
		}
	} else {
		uuid, err = o.nbctl("create", "dhcp_option",
			fmt.Sprintf("external_ids:lxd_switch=%s", string(switchName)),
			fmt.Sprintf("cidr=%s", subnet.String()),
		)
		if err != nil {
			return err
		}

		uuid = strings.TrimSpace(uuid)
	}

	// We have to use dhcp-options-set-options rather than the command above as its the only way to allow the
	// domain_name option to be properly escaped.
	args := []string{"dhcp-options-set-options", uuid,
		fmt.Sprintf("server_id=%s", opts.ServerID.String()),
		fmt.Sprintf("server_mac=%s", opts.ServerMAC.String()),
		fmt.Sprintf("lease_time=%d", opts.LeaseTime/time.Second),
	}

	if opts.Router != nil {
		args = append(args, fmt.Sprintf("router=%s", opts.Router.String()))
	}

	if opts.RecursiveDNSServer != nil {
		nsIPs := make([]string, 0, len(opts.RecursiveDNSServer))
		for _, nsIP := range opts.RecursiveDNSServer {
			if nsIP.To4() == nil {
				continue // Only include IPv4 addresses.
			}

			nsIPs = append(nsIPs, nsIP.String())
		}

		args = append(args, fmt.Sprintf("dns_server={%s}", strings.Join(nsIPs, ",")))
	}

	if opts.DomainName != "" {
		// Special quoting to allow domain names.
		args = append(args, fmt.Sprintf(`domain_name="%s"`, opts.DomainName))
	}

	if opts.MTU > 0 {
		args = append(args, fmt.Sprintf("mtu=%d", opts.MTU))
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
func (o *OVN) LogicalSwitchDHCPv6OptionsSet(switchName OVNSwitch, uuid string, subnet *net.IPNet, opts *OVNDHCPv6Opts) error {
	var err error

	if uuid != "" {
		_, err = o.nbctl("set", "dhcp_option", uuid,
			fmt.Sprintf("external_ids:lxd_switch=%s", string(switchName)),
			fmt.Sprintf(`cidr="%s"`, subnet.String()), // Special quoting to allow IPv6 address.
		)
		if err != nil {
			return err
		}
	} else {
		uuid, err = o.nbctl("create", "dhcp_option",
			fmt.Sprintf("external_ids:lxd_switch=%s", string(switchName)),
			fmt.Sprintf(`cidr="%s"`, subnet.String()), // Special quoting to allow IPv6 address.
		)
		if err != nil {
			return err
		}

		uuid = strings.TrimSpace(uuid)
	}

	// We have to use dhcp-options-set-options rather than the command above as its the only way to allow the
	// domain_name option to be properly escaped.
	args := []string{"dhcp-options-set-options", uuid,
		fmt.Sprintf("server_id=%s", opts.ServerID.String()),
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

// LogicalSwitchDHCPOptionsGetID returns the UUID for DHCP options set associated to the logical switch and subnet.
func (o *OVN) LogicalSwitchDHCPOptionsGetID(switchName OVNSwitch, subnet *net.IPNet) (string, error) {
	uuid, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--colum=_uuid", "find", "dhcp_options",
		fmt.Sprintf("external_ids:lxd_switch=%s", string(switchName)),
		fmt.Sprintf(`cidr="%s"`, subnet.String()), // Special quoting to support IPv6 subnets.
	)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(uuid), nil
}

// LogicalSwitchDHCPOptionsGet retrieves the existing DHCP options defined for a logical switch.
func (o *OVN) LogicalSwitchDHCPOptionsGet(switchName OVNSwitch) ([]OVNDHCPOptsSet, error) {
	output, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--colum=_uuid,cidr", "find", "dhcp_options",
		fmt.Sprintf("external_ids:lxd_switch=%s", string(switchName)),
	)
	if err != nil {
		return nil, err
	}

	colCount := 2
	dhcpOpts := []OVNDHCPOptsSet{}
	output = strings.TrimSpace(output)
	if output != "" {
		for _, row := range strings.Split(output, "\n") {
			rowParts := strings.SplitN(row, ",", colCount)
			if len(rowParts) < colCount {
				return nil, fmt.Errorf("Too few columns in output")
			}

			_, cidr, err := net.ParseCIDR(rowParts[1])
			if err != nil {
				return nil, err
			}

			dhcpOpts = append(dhcpOpts, OVNDHCPOptsSet{
				UUID: rowParts[0],
				CIDR: cidr,
			})
		}
	}

	return dhcpOpts, nil
}

// LogicalSwitchDHCPOptionsDelete deletes any DHCP options defined for a switch.
// Optionally accepts one or more specific UUID records to delete (if they are associated to the specified switch).
func (o *OVN) LogicalSwitchDHCPOptionsDelete(switchName OVNSwitch, onlyUUID ...string) error {
	existingOpts, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--colum=_uuid", "find", "dhcp_options",
		fmt.Sprintf("external_ids:lxd_switch=%s", string(switchName)),
	)
	if err != nil {
		return err
	}

	shouldDelete := func(existingUUID string) bool {
		if len(onlyUUID) <= 0 {
			return true // Delete all records if no UUID filter supplied.
		}

		for _, uuid := range onlyUUID {
			if existingUUID == uuid {
				return true
			}
		}

		return false
	}

	existingOpts = strings.TrimSpace(existingOpts)
	if existingOpts != "" {
		for _, uuid := range strings.Split(existingOpts, "\n") {
			if shouldDelete(uuid) {
				_, err = o.nbctl("destroy", "dhcp_options", uuid)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// logicalSwitchDNSRecordsDelete deletes any DNS records defined for a switch.
func (o *OVN) logicalSwitchDNSRecordsDelete(switchName OVNSwitch) error {
	existingOpts, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--colum=_uuid", "find", "dns",
		fmt.Sprintf("external_ids:lxd_switch=%s", string(switchName)),
	)
	if err != nil {
		return err
	}

	existingOpts = strings.TrimSpace(existingOpts)
	if existingOpts != "" {
		for _, uuid := range strings.Split(existingOpts, "\n") {
			_, err = o.nbctl("destroy", "dns", uuid)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// LogicalSwitchPortAdd adds a named logical switch port to a logical switch.
// If mayExist is true, then an existing resource of the same name is not treated as an error.
func (o *OVN) LogicalSwitchPortAdd(switchName OVNSwitch, portName OVNSwitchPort, mayExist bool) error {
	args := []string{}

	if mayExist {
		args = append(args, "--may-exist")
	}

	args = append(args, "lsp-add", string(switchName), string(portName))

	_, err := o.nbctl(args...)
	if err != nil {
		return err
	}

	return nil
}

// LogicalSwitchPortSet sets options on logical switch port.
func (o *OVN) LogicalSwitchPortSet(portName OVNSwitchPort, opts *OVNSwitchPortOpts) error {
	ipStr := make([]string, 0, len(opts.IPs))
	for _, ip := range opts.IPs {
		ipStr = append(ipStr, ip.String())
	}

	var addresses string
	if opts.MAC != nil && len(ipStr) > 0 {
		addresses = fmt.Sprintf("%s %s", opts.MAC.String(), strings.Join(ipStr, " "))
	} else if opts.MAC != nil && len(ipStr) <= 0 {
		addresses = fmt.Sprintf("%s %s", opts.MAC.String(), "dynamic")
	} else {
		addresses = "dynamic"
	}

	_, err := o.nbctl("lsp-set-addresses", string(portName), addresses)
	if err != nil {
		return err
	}

	if opts.DHCPv4OptsID != "" {
		_, err = o.nbctl("lsp-set-dhcpv4-options", string(portName), opts.DHCPv4OptsID)
		if err != nil {
			return err
		}
	}

	if opts.DHCPv6OptsID != "" {
		_, err = o.nbctl("lsp-set-dhcpv6-options", string(portName), opts.DHCPv6OptsID)
		if err != nil {
			return err
		}
	}

	return nil
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

	dynamicAddressesRaw, err = strconv.Unquote(dynamicAddressesRaw)
	if err != nil {
		return nil, err
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

// LogicalSwitchPortSetDNS sets up the switch DNS records for the DNS name resolving to the IPs of the switch port.
// Attempts to find at most one IP for each IP protocol, preferring static addresses over dynamic.
// Returns the DNS record UUID, IPv4 and IPv6 addresses used for DNS records.
func (o *OVN) LogicalSwitchPortSetDNS(switchName OVNSwitch, portName OVNSwitchPort, dnsName string) (string, net.IP, net.IP, error) {
	var dnsIPv4, dnsIPv6 net.IP

	// checkAndStoreIP checks if the supplied IP is valid and can be used for a missing DNS IP variable.
	// If the found IP is needed, stores into the relevant dnsIPvP{X} variable and returns true.
	checkAndStoreIP := func(ip net.IP) bool {
		if ip != nil {
			isV4 := ip.To4() != nil
			if dnsIPv4 == nil && isV4 {
				dnsIPv4 = ip
				return true
			} else if dnsIPv6 == nil && !isV4 {
				dnsIPv6 = ip
				return true
			}
		}

		return false
	}

	// Get static and dynamic IPs for switch port.
	staticAddressesRaw, err := o.nbctl("lsp-get-addresses", string(portName))
	if err != nil {
		return "", nil, nil, err
	}

	staticAddresses := strings.Split(strings.TrimSpace(staticAddressesRaw), " ")
	hasDynamic := false
	for _, staticAddress := range staticAddresses {
		// Record that there should be at least one dynamic address (may be a MAC address though).
		if staticAddress == "dynamic" {
			hasDynamic = true
			continue
		}

		// Try and find the first IPv4 and IPv6 addresses from the static address list.
		if checkAndStoreIP(net.ParseIP(staticAddress)) {
			if dnsIPv4 != nil && dnsIPv6 != nil {
				break // We've found all we wanted.
			}
		}
	}

	// Get dynamic IPs for switch port if indicated and needed.
	if hasDynamic && (dnsIPv4 == nil || dnsIPv6 == nil) {
		dynamicIPs, err := o.LogicalSwitchPortDynamicIPs(portName)
		if err != nil {
			return "", nil, nil, err
		}

		for _, dynamicIP := range dynamicIPs {
			// Try and find the first IPv4 and IPv6 addresses from the dynamic address list.
			if checkAndStoreIP(dynamicIP) {
				if dnsIPv4 != nil && dnsIPv6 != nil {
					break // We've found all we wanted.
				}
			}
		}
	}

	// Create a list of IPs for the DNS record.
	dnsIPs := make([]string, 0, 2)
	if dnsIPv4 != nil {
		dnsIPs = append(dnsIPs, dnsIPv4.String())
	}

	if dnsIPv6 != nil {
		dnsIPs = append(dnsIPs, dnsIPv6.String())
	}

	if len(dnsIPs) <= 0 {
		return "", nil, nil, ErrOVNNoPortIPs
	}

	// Check if existing DNS record exists for switch port.
	dnsUUID, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--colum=_uuid", "find", "dns",
		fmt.Sprintf("external_ids:lxd_switch_port=%s", string(portName)),
	)
	if err != nil {
		return "", nil, nil, err
	}

	cmdArgs := []string{
		fmt.Sprintf(`records={"%s"="%s"}`, dnsName, strings.Join(dnsIPs, " ")),
		fmt.Sprintf("external_ids:lxd_switch=%s", string(switchName)),
		fmt.Sprintf("external_ids:lxd_switch_port=%s", string(portName)),
	}

	dnsUUID = strings.TrimSpace(dnsUUID)
	if dnsUUID != "" {
		// Update existing record if exists.
		_, err = o.nbctl(append([]string{"set", "dns", dnsUUID}, cmdArgs...)...)
		if err != nil {
			return "", nil, nil, err
		}
	} else {
		// Create new record if needed.
		dnsUUID, err = o.nbctl(append([]string{"create", "dns"}, cmdArgs...)...)
		if err != nil {
			return "", nil, nil, err
		}
		dnsUUID = strings.TrimSpace(dnsUUID)
	}

	// Add DNS record to switch DNS records.
	_, err = o.nbctl("add", "logical_switch", string(switchName), "dns_records", dnsUUID)
	if err != nil {
		return "", nil, nil, err
	}

	return dnsUUID, dnsIPv4, dnsIPv6, nil
}

// LogicalSwitchPortGetDNS returns the logical switch port DNS info (UUID, name and IPs).
func (o *OVN) LogicalSwitchPortGetDNS(portName OVNSwitchPort) (string, string, []net.IP, error) {
	// Get UUID and DNS IPs for a switch port in the format: "<DNS UUID>,<DNS NAME>=<IP> <IP>"
	output, err := o.nbctl("--format=csv", "--no-headings", "--data=bare", "--colum=_uuid,records", "find", "dns",
		fmt.Sprintf("external_ids:lxd_switch_port=%s", string(portName)),
	)
	if err != nil {
		return "", "", nil, err
	}

	parts := strings.Split(strings.TrimSpace(output), ",")
	dnsUUID := strings.TrimSpace(parts[0])

	var dnsName string
	var ips []net.IP

	// Try and parse the DNS name and IPs.
	if len(parts) > 1 {
		dnsParts := strings.SplitN(strings.TrimSpace(parts[1]), "=", 2)
		if len(dnsParts) == 2 {
			dnsName = strings.TrimSpace(dnsParts[0])
			ipParts := strings.Split(dnsParts[1], " ")
			for _, ipPart := range ipParts {
				ip := net.ParseIP(strings.TrimSpace(ipPart))
				if ip != nil {
					ips = append(ips, ip)
				}
			}
		}

	}

	return dnsUUID, dnsName, ips, nil
}

// LogicalSwitchPortDeleteDNS removes DNS records for a switch port.
func (o *OVN) LogicalSwitchPortDeleteDNS(switchName OVNSwitch, dnsUUID string) error {
	// Remove DNS record association from switch.
	_, err := o.nbctl("remove", "logical_switch", string(switchName), "dns_records", dnsUUID)
	if err != nil {
		return err
	}

	// Remove DNS record entry itself.
	_, err = o.nbctl("destroy", "dns", dnsUUID)
	if err != nil {
		return err
	}

	return nil
}

// LogicalSwitchPortDelete deletes a named logical switch port.
func (o *OVN) LogicalSwitchPortDelete(portName OVNSwitchPort) error {
	_, err := o.nbctl("--if-exists", "lsp-del", string(portName))
	if err != nil {
		return err
	}

	return nil
}

// LogicalSwitchPortLinkRouter links a logical switch port to a logical router port.
func (o *OVN) LogicalSwitchPortLinkRouter(switchPortName OVNSwitchPort, routerPortName OVNRouterPort) error {
	// Connect logical router port to switch.
	_, err := o.nbctl("lsp-set-type", string(switchPortName), "router")
	if err != nil {
		return err
	}

	_, err = o.nbctl("lsp-set-addresses", string(switchPortName), "router")
	if err != nil {
		return err
	}

	_, err = o.nbctl("lsp-set-options", string(switchPortName),
		fmt.Sprintf("nat-addresses=%s", "router"),
		fmt.Sprintf("router-port=%s", string(routerPortName)),
	)
	if err != nil {
		return err
	}

	return nil
}

// LogicalSwitchPortLinkProviderNetwork links a logical switch port to a provider network.
func (o *OVN) LogicalSwitchPortLinkProviderNetwork(switchPortName OVNSwitchPort, extNetworkName string) error {
	// Forward any unknown MAC frames down this port.
	_, err := o.nbctl("lsp-set-addresses", string(switchPortName), "unknown")
	if err != nil {
		return err
	}

	_, err = o.nbctl("lsp-set-type", string(switchPortName), "localnet")
	if err != nil {
		return err
	}

	_, err = o.nbctl("lsp-set-options", string(switchPortName), fmt.Sprintf("network_name=%s", extNetworkName))
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
	existing, err := o.nbctl("--no-headings", "--data=bare", "--colum=name", "find", "ha_chassis_group", fmt.Sprintf("name=%s", string(haChassisGroupName)))
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
	_, err := o.nbctl("ha-chassis-group-add-chassis", string(haChassisGroupName), chassisID, fmt.Sprintf("%d", priority))
	if err != nil {
		return err
	}

	return nil
}

// ChassisGroupChassisDelete deletes a chassis ID from an HA chassis group.
func (o *OVN) ChassisGroupChassisDelete(haChassisGroupName OVNChassisGroup, chassisID string) error {
	// Check if chassis group exists. ovn-nbctl doesn't provide an "--if-exists" option for this.
	existingChassisGroup, err := o.nbctl("--no-headings", "--data=bare", "--colum=name", "find", "ha_chassis_group", fmt.Sprintf("name=%s", string(haChassisGroupName)))
	if err != nil {
		return err
	}

	// Nothing to do if chassis group doesn't exist.
	if strings.TrimSpace(existingChassisGroup) == "" {
		return nil
	}

	// Check if chassis exists. ovn-nbctl doesn't provide an "--if-exists" option for this.
	existingChassis, err := o.nbctl("--no-headings", "--data=bare", "--colum=chassis_name", "find", "ha_chassis", fmt.Sprintf("chassis_name=%s", chassisID))
	if err != nil {
		return err
	}

	// Remove chassis from group if exists.
	if strings.TrimSpace(existingChassis) != "" {
		_, err := o.nbctl("ha-chassis-group-remove-chassis", string(haChassisGroupName), chassisID)
		if err != nil {
			return err
		}
	}

	return nil
}
