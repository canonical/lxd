package device

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"math"
	"math/big"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/mdlayher/eui64"

	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/dnsmasq"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/iptables"
	"github.com/lxc/lxd/shared"
)

// dhcpAllocation represents an IP allocation from dnsmasq used for IP filtering.
type dhcpAllocation struct {
	IP     net.IP
	Name   string
	MAC    net.HardwareAddr
	Static bool
}

// dhcpRange represents a range of IPs from start to end.
type dhcpRange struct {
	Start net.IP
	End   net.IP
}

type nicBridged struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *nicBridged) validateConfig() error {
	if d.instance.Type() != instance.TypeContainer {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{"parent"}
	optionalFields := []string{
		"name",
		"mtu",
		"hwaddr",
		"host_name",
		"limits.ingress",
		"limits.egress",
		"limits.max",
		"ipv4.address",
		"ipv6.address",
		"ipv4.routes",
		"ipv6.routes",
		"security.mac_filtering",
		"security.ipv4_filtering",
		"security.ipv6_filtering",
		"maas.subnet.ipv4",
		"maas.subnet.ipv6",
	}
	err := config.ValidateDevice(nicValidationRules(requiredFields, optionalFields), d.config)
	if err != nil {
		return err
	}

	// If parent isn't a managed network, check that no managed-only features are enabled.
	if !shared.PathExists(shared.VarPath("networks", d.config["parent"], "dnsmasq.pid")) {
		for _, k := range []string{"ipv4.address", "ipv6.address", "security.mac_filtering", "security.ipv4_filtering", "security.ipv6_filtering"} {
			if d.config[k] != "" || shared.IsTrue(d.config[k]) {
				return fmt.Errorf("%s can only be used with managed networks", k)
			}
		}
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicBridged) validateEnvironment() error {
	if d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

// CanHotPlug returns whether the device can be managed whilst the instance is running, it also
// returns a list of fields that can be updated without triggering a device remove & add.
func (d *nicBridged) CanHotPlug() (bool, []string) {
	return true, []string{"limits.ingress", "limits.egress", "limits.max", "ipv4.routes", "ipv6.routes", "ipv4.address", "ipv6.address", "security.mac_filtering", "security.ipv4_filtering", "security.ipv6_filtering"}
}

// Add is run when a device is added to an instance whether or not the instance is running.
func (d *nicBridged) Add() error {
	// Rebuild dnsmasq entry if needed and reload.
	err := d.rebuildDnsmasqEntry()
	if err != nil {
		return err
	}

	return nil
}

// Start is run when the device is added to the instance and instance is starting or running.
func (d *nicBridged) Start() (*RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	saveData := make(map[string]string)
	saveData["host_name"] = d.config["host_name"]
	if saveData["host_name"] == "" {
		saveData["host_name"] = NetworkRandomDevName("veth")
	}

	// Create veth pair and configure the peer end with custom hwaddr and mtu if supplied.
	peerName, err := networkCreateVethPair(saveData["host_name"], d.config)
	if err != nil {
		return nil, err
	}

	// Apply and host-side limits and routes.
	err = networkSetupHostVethDevice(d.config, nil, saveData)
	if err != nil {
		NetworkRemoveInterface(saveData["host_name"])
		return nil, err
	}

	// Apply and host-side network filters (uses enriched host_name from networkSetupHostVethDevice).
	err = d.setupHostFilters(nil)
	if err != nil {
		NetworkRemoveInterface(saveData["host_name"])
		return nil, err
	}

	// Attach host side veth interface to bridge.
	err = NetworkAttachInterface(d.config["parent"], saveData["host_name"])
	if err != nil {
		NetworkRemoveInterface(saveData["host_name"])
		return nil, err
	}

	// Attempt to disable router advertisement acceptance.
	err = NetworkSysctlSet(fmt.Sprintf("ipv6/conf/%s/accept_ra", saveData["host_name"]), "0")
	if err != nil && !os.IsNotExist(err) {
		NetworkRemoveInterface(saveData["host_name"])
		return nil, err
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := RunConfig{}
	runConf.NetworkInterfaces = [][]RunConfigItem{{
		{Key: "name", Value: d.config["name"]},
		{Key: "type", Value: "phys"},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: peerName},
	}}

	return &runConf, nil
}

// Update applies configuration changes to a started device.
func (d *nicBridged) Update(oldConfig config.Device, isRunning bool) error {
	// If an IPv6 address has changed, flush all existing IPv6 leases for instance so instance
	// isn't allocated old IP. This is important with IPv6 because DHCPv6 supports multiple IP
	// address allocation and would result in instance having leases for both old and new IPs.
	if d.config["hwaddr"] != "" && d.config["ipv6.address"] != oldConfig["ipv6.address"] {
		err := d.networkClearLease(d.instance.Name(), d.config["parent"], d.config["hwaddr"], clearLeaseIPv6Only)
		if err != nil {
			return err
		}
	}

	v := d.volatileGet()

	// If instance is running, apply host side limits and filters first before rebuilding
	// dnsmasq config below so that existing config can be used as part of the filter removal.
	if isRunning {
		err := d.validateEnvironment()
		if err != nil {
			return err
		}

		// Apply and host-side limits and routes.
		err = networkSetupHostVethDevice(d.config, oldConfig, v)
		if err != nil {
			return err
		}

		// Apply and host-side network filters (uses enriched host_name from networkSetupHostVethDevice).
		err = d.setupHostFilters(oldConfig)
		if err != nil {
			return err
		}
	}

	// Rebuild dnsmasq entry if needed and reload.
	err := d.rebuildDnsmasqEntry()
	if err != nil {
		return err
	}

	// If an IPv6 address has changed, if the instance is running we should bounce the host-side
	// veth interface to give the instance a chance to detect the change and re-apply for an
	// updated lease with new IP address.
	if d.config["ipv6.address"] != oldConfig["ipv6.address"] && v["host_name"] != "" && shared.PathExists(fmt.Sprintf("/sys/class/net/%s", v["host_name"])) {
		_, err := shared.RunCommand("ip", "link", "set", v["host_name"], "down")
		if err != nil {
			return err
		}
		_, err = shared.RunCommand("ip", "link", "set", v["host_name"], "up")
		if err != nil {
			return err
		}
	}

	return nil
}

// Stop is run when the device is removed from the instance.
func (d *nicBridged) Stop() error {
	defer d.volatileSet(map[string]string{
		"host_name": "",
	})

	v := d.volatileGet()

	if d.config["host_name"] == "" {
		d.config["host_name"] = v["host_name"]
	}

	if d.config["hwaddr"] == "" {
		d.config["hwaddr"] = v["hwaddr"]
	}

	if d.config["host_name"] != "" && shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["host_name"])) {
		// Removing host-side end of veth pair will delete the peer end too.
		err := NetworkRemoveInterface(d.config["host_name"])
		if err != nil {
			return fmt.Errorf("Failed to remove interface %s: %s", d.config["host_name"], err)
		}
	}

	networkRemoveVethRoutes(d.config)
	d.removeFilters(d.config)

	return nil
}

// Remove is run when the instance is deleted.
func (d *nicBridged) Remove() error {
	err := d.networkClearLease(d.instance.Name(), d.config["parent"], d.config["hwaddr"], clearLeaseAll)
	if err != nil {
		return err
	}

	// If device was on managed parent, remove old config file.
	if d.config["parent"] != "" && shared.PathExists(shared.VarPath("networks", d.config["parent"], "dnsmasq.pid")) {
		dnsmasq.ConfigMutex.Lock()
		defer dnsmasq.ConfigMutex.Unlock()

		err := dnsmasq.RemoveStaticEntry(d.config["parent"], d.instance.Project(), d.instance.Name())
		if err != nil {
			return err
		}

		// Reload dnsmasq to apply new settings.
		err = dnsmasq.Kill(d.config["parent"], true)
		if err != nil {
			return err
		}
	}

	return nil
}

// rebuildDnsmasqEntry rebuilds the dnsmasq host entry if connected to an LXD managed network
// and reloads dnsmasq.
func (d *nicBridged) rebuildDnsmasqEntry() error {
	// Rebuild dnsmasq config if a bridged device has changed and parent is a managed network.
	if !shared.PathExists(shared.VarPath("networks", d.config["parent"], "dnsmasq.pid")) {
		return nil
	}

	dnsmasq.ConfigMutex.Lock()
	defer dnsmasq.ConfigMutex.Unlock()

	_, dbInfo, err := d.state.Cluster.NetworkGet(d.config["parent"])
	if err != nil {
		return err
	}

	netConfig := dbInfo.Config
	ipv4Address := d.config["ipv4.address"]
	ipv6Address := d.config["ipv6.address"]

	// If IP filtering is enabled, and no static IP in config, check if there is already a
	// dynamically assigned static IP in dnsmasq config and write that back out in new config.
	if (shared.IsTrue(d.config["security.ipv4_filtering"]) && ipv4Address == "") || (shared.IsTrue(d.config["security.ipv6_filtering"]) && ipv6Address == "") {
		curIPv4, curIPv6, err := dnsmasq.DHCPStaticIPs(d.config["parent"], d.instance.Name())
		if err != nil && !os.IsNotExist(err) {
			return err
		}

		if ipv4Address == "" && curIPv4.IP != nil {
			ipv4Address = curIPv4.IP.String()
		}

		if ipv6Address == "" && curIPv6.IP != nil {
			ipv6Address = curIPv6.IP.String()
		}
	}

	err = dnsmasq.UpdateStaticEntry(d.config["parent"], d.instance.Project(), d.instance.Name(), netConfig, d.config["hwaddr"], ipv4Address, ipv6Address)
	if err != nil {
		return err
	}

	// Reload dnsmasq to apply new settings.
	err = dnsmasq.Kill(d.config["parent"], true)
	if err != nil {
		return err
	}

	return nil
}

// setupHostFilters applies any host side network filters.
func (d *nicBridged) setupHostFilters(oldConfig config.Device) error {
	// Remove any old network filters if non-empty oldConfig supplied as part of update.
	if oldConfig != nil && (shared.IsTrue(oldConfig["security.mac_filtering"]) || shared.IsTrue(oldConfig["security.ipv4_filtering"]) || shared.IsTrue(oldConfig["security.ipv6_filtering"])) {
		d.removeFilters(oldConfig)
	}

	// Setup network filters.
	if shared.IsTrue(d.config["security.mac_filtering"]) || shared.IsTrue(d.config["security.ipv4_filtering"]) || shared.IsTrue(d.config["security.ipv6_filtering"]) {
		err := d.setFilters()
		if err != nil {
			return err
		}
	}

	return nil
}

// removeFilters removes any network level filters defined for the instance.
func (d *nicBridged) removeFilters(m config.Device) error {
	if m["hwaddr"] == "" {
		return fmt.Errorf("Failed to remove network filters for %s: hwaddr not defined", m["name"])
	}

	if m["host_name"] == "" {
		return fmt.Errorf("Failed to remove network filters for %s: host_name not defined", m["name"])
	}

	// Remove any IPv6 filters used for this instance.
	err := iptables.ContainerClear("ipv6", fmt.Sprintf("%s - ipv6_filtering", d.instance.Name()), "filter")
	if err != nil {
		return fmt.Errorf("Failed to clear ip6tables ipv6_filter rules for %s: %v", m["name"], err)
	}

	// Read current static IP allocation configured from dnsmasq host config (if exists).
	IPv4, IPv6, err := d.getDHCPStaticIPs(m["parent"], d.instance.Name())
	if err != nil {
		return fmt.Errorf("Failed to remove network filters for %s: %v", m["name"], err)
	}

	// Get a current list of rules active on the host.
	out, err := shared.RunCommand("ebtables", "-L", "--Lmac2", "--Lx")
	if err != nil {
		return fmt.Errorf("Failed to remove network filters for %s: %v", m["name"], err)
	}

	// Get a list of rules that we would have applied on instance start.
	rules := d.generateFilterEbtablesRules(m, IPv4.IP, IPv6.IP)

	errs := []error{}
	// Iterate through each active rule on the host and try and match it to one the LXD rules.
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		fieldsLen := len(fields)

		for _, rule := range rules {
			// Rule doesn't match if the field lenths aren't the same, move on.
			if len(rule) != fieldsLen {
				continue
			}

			// Check whether active rule matches one of our rules to delete.
			if !d.matchEbtablesRule(fields, rule, true) {
				continue
			}

			// If we get this far, then the current host rule matches one of our LXD
			// rules, so we should run the modified command to delete it.
			_, err = shared.RunCommand(fields[0], fields[1:]...)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("Failed to remove network filters rule for %s: %v", m["name"], errs)
	}

	return nil
}

// getDHCPStaticContainerIPs retrieves the dnsmasq statically allocated IPs for a instance.
// Returns IPv4 and IPv6 dhcpAllocation structs respectively.
func (d *nicBridged) getDHCPStaticIPs(network string, instanceName string) (dhcpAllocation, dhcpAllocation, error) {
	var IPv4, IPv6 dhcpAllocation

	file, err := os.Open(shared.VarPath("networks", network, "dnsmasq.hosts") + "/" + instanceName)
	if err != nil {
		return IPv4, IPv6, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.SplitN(scanner.Text(), ",", -1)
		for _, field := range fields {
			// Check if field is IPv4 or IPv6 address.
			if strings.Count(field, ".") == 3 {
				IP := net.ParseIP(field)
				if IP.To4() == nil {
					return IPv4, IPv6, fmt.Errorf("Error parsing IP address: %v", field)
				}
				IPv4 = dhcpAllocation{Name: d.instance.Name(), Static: true, IP: IP.To4()}

			} else if strings.HasPrefix(field, "[") && strings.HasSuffix(field, "]") {
				IP := net.ParseIP(field[1 : len(field)-1])
				if IP == nil {
					return IPv4, IPv6, fmt.Errorf("Error parsing IP address: %v", field)
				}
				IPv6 = dhcpAllocation{Name: d.instance.Name(), Static: true, IP: IP}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return IPv4, IPv6, err
	}

	return IPv4, IPv6, nil
}

// generateFilterEbtablesRules returns a customised set of ebtables filter rules based on the device.
func (d *nicBridged) generateFilterEbtablesRules(m config.Device, IPv4 net.IP, IPv6 net.IP) [][]string {
	// MAC source filtering rules. Blocks any packet coming from instance with an incorrect Ethernet source MAC.
	// This is required for IP filtering too.
	rules := [][]string{
		{"ebtables", "-t", "filter", "-A", "INPUT", "-s", "!", m["hwaddr"], "-i", m["host_name"], "-j", "DROP"},
		{"ebtables", "-t", "filter", "-A", "FORWARD", "-s", "!", m["hwaddr"], "-i", m["host_name"], "-j", "DROP"},
	}

	if shared.IsTrue(m["security.ipv4_filtering"]) && IPv4 != nil {
		rules = append(rules,
			// Prevent ARP MAC spoofing (prevents the instance poisoning the ARP cache of its neighbours with a MAC address that isn't its own).
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "ARP", "-i", m["host_name"], "--arp-mac-src", "!", m["hwaddr"], "-j", "DROP"},
			[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "ARP", "-i", m["host_name"], "--arp-mac-src", "!", m["hwaddr"], "-j", "DROP"},
			// Prevent ARP IP spoofing (prevents the instance redirecting traffic for IPs that are not its own).
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "ARP", "-i", m["host_name"], "--arp-ip-src", "!", IPv4.String(), "-j", "DROP"},
			[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "ARP", "-i", m["host_name"], "--arp-ip-src", "!", IPv4.String(), "-j", "DROP"},
			// Allow DHCPv4 to the host only. This must come before the IP source filtering rules below.
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv4", "-s", m["hwaddr"], "-i", m["host_name"], "--ip-src", "0.0.0.0", "--ip-dst", "255.255.255.255", "--ip-proto", "udp", "--ip-dport", "67", "-j", "ACCEPT"},
			// IP source filtering rules. Blocks any packet coming from instance with an incorrect IP source address.
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv4", "-i", m["host_name"], "--ip-src", "!", IPv4.String(), "-j", "DROP"},
			[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "IPv4", "-i", m["host_name"], "--ip-src", "!", IPv4.String(), "-j", "DROP"},
		)
	}

	if shared.IsTrue(m["security.ipv6_filtering"]) && IPv6 != nil {
		rules = append(rules,
			// Allow DHCPv6 and Router Solicitation to the host only. This must come before the IP source filtering rules below.
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv6", "-s", m["hwaddr"], "-i", m["host_name"], "--ip6-src", "fe80::/ffc0::", "--ip6-dst", "ff02::1:2/ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "--ip6-proto", "udp", "--ip6-dport", "547", "-j", "ACCEPT"},
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv6", "-s", m["hwaddr"], "-i", m["host_name"], "--ip6-src", "fe80::/ffc0::", "--ip6-dst", "ff02::2/ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "--ip6-proto", "ipv6-icmp", "--ip6-icmp-type", "router-solicitation", "-j", "ACCEPT"},
			// IP source filtering rules. Blocks any packet coming from instance with an incorrect IP source address.
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv6", "-i", m["host_name"], "--ip6-src", "!", fmt.Sprintf("%s/ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", IPv6.String()), "-j", "DROP"},
			[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "IPv6", "-i", m["host_name"], "--ip6-src", "!", fmt.Sprintf("%s/ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", IPv6.String()), "-j", "DROP"},
		)
	}

	return rules
}

// matchEbtablesRule compares an active rule to a supplied match rule to see if they match.
// If deleteMode is true then the "-A" flag in the active rule will be modified to "-D" and will
// not be part of the equality match. This allows delete commands to be generated from dumped add commands.
func (d *nicBridged) matchEbtablesRule(activeRule []string, matchRule []string, deleteMode bool) bool {
	for i := range matchRule {
		// Active rules will be dumped in "add" format, we need to detect
		// this and switch it to "delete" mode if requested. If this has already been
		// done then move on, as we don't want to break the comparison below.
		if deleteMode && (activeRule[i] == "-A" || activeRule[i] == "-D") {
			activeRule[i] = "-D"
			continue
		}

		// Check the match rule field matches the active rule field.
		// If they don't match, then this isn't one of our rules.
		if activeRule[i] != matchRule[i] {
			return false
		}
	}

	return true
}

// setFilters sets up any network level filters defined for the instance.
// These are controlled by the security.mac_filtering, security.ipv4_Filtering and security.ipv6_filtering config keys.
func (d *nicBridged) setFilters() (err error) {
	if d.config["hwaddr"] == "" {
		return fmt.Errorf("Failed to set network filters: require hwaddr defined")
	}

	if d.config["host_name"] == "" {
		return fmt.Errorf("Failed to set network filters: require host_name defined")
	}

	if d.config["parent"] == "" {
		return fmt.Errorf("Failed to set network filters: require parent defined")
	}

	if shared.IsTrue(d.config["security.ipv6_filtering"]) {
		// Check br_netfilter is loaded and enabled for IPv6.
		sysctlPath := "bridge/bridge-nf-call-ip6tables"
		sysctlVal, err := NetworkSysctlGet(sysctlPath)
		if err != nil {
			return fmt.Errorf("Error reading net sysctl %s: %v", sysctlPath, err)
		}

		if sysctlVal != "1\n" {
			return fmt.Errorf("security.ipv6_filtering requires br_netfilter and sysctl net.bridge.bridge-nf-call-ip6tables=1")
		}
	}

	// Retrieve existing IPs, or allocate new ones if needed.
	IPv4, IPv6, err := d.allocateFilterIPs()
	if err != nil {
		return err
	}

	// If anything goes wrong, clean up so we don't leave orphaned rules.
	defer func() {
		if err != nil {
			d.removeFilters(d.config)
		}
	}()

	rules := d.generateFilterEbtablesRules(d.config, IPv4, IPv6)
	for _, rule := range rules {
		_, err = shared.RunCommand(rule[0], rule[1:]...)
		if err != nil {
			return err
		}
	}

	rules, err = d.generateFilterIptablesRules(d.config, IPv6)
	if err != nil {
		return err
	}

	for _, rule := range rules {
		err = iptables.ContainerPrepend(rule[0], fmt.Sprintf("%s - %s_filtering", d.instance.Name(), rule[0]), "filter", rule[1], rule[2:]...)
		if err != nil {
			return err
		}
	}

	return nil
}

// networkAllocateVethFilterIPs retrieves previously allocated IPs, or allocate new ones if needed.
func (d *nicBridged) allocateFilterIPs() (net.IP, net.IP, error) {
	var IPv4, IPv6 net.IP

	// Check if there is a valid static IPv4 address defined.
	if d.config["ipv4.address"] != "" {
		IPv4 = net.ParseIP(d.config["ipv4.address"])
		if IPv4 == nil {
			return nil, nil, fmt.Errorf("Invalid static IPv4 address %s", d.config["ipv4.address"])
		}
	}

	// Check if there is a valid static IPv6 address defined.
	if d.config["ipv6.address"] != "" {
		IPv6 = net.ParseIP(d.config["ipv6.address"])
		if IPv6 == nil {
			return nil, nil, fmt.Errorf("Invalid static IPv6 address %s", d.config["ipv6.address"])
		}
	}

	_, dbInfo, err := d.state.Cluster.NetworkGet(d.config["parent"])
	if err != nil {
		return nil, nil, err
	}

	netConfig := dbInfo.Config

	// Check DHCPv4 is enabled on parent if dynamic IPv4 allocation is needed.
	if shared.IsTrue(d.config["security.ipv4_filtering"]) && IPv4 == nil && netConfig["ipv4.dhcp"] != "" && !shared.IsTrue(netConfig["ipv4.dhcp"]) {
		return nil, nil, fmt.Errorf("Cannot use security.ipv4_filtering as DHCPv4 is disabled on parent %s and no static IPv4 address set", d.config["parent"])
	}

	// Check DHCPv6 is enabled on parent if dynamic IPv6 allocation is needed.
	if shared.IsTrue(d.config["security.ipv6_filtering"]) && IPv6 == nil && netConfig["ipv6.dhcp"] != "" && !shared.IsTrue(netConfig["ipv6.dhcp"]) {
		return nil, nil, fmt.Errorf("Cannot use security.ipv6_filtering as DHCPv6 is disabled on parent %s and no static IPv6 address set", d.config["parent"])
	}

	dnsmasq.ConfigMutex.Lock()
	defer dnsmasq.ConfigMutex.Unlock()

	// Read current static IP allocation configured from dnsmasq host config (if exists).
	curIPv4, curIPv6, err := d.getDHCPStaticIPs(d.config["parent"], d.instance.Name())
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}

	// If no static IPv4, then check if there is a valid static DHCP IPv4 address defined.
	if IPv4 == nil && curIPv4.IP != nil {
		_, subnet, err := net.ParseCIDR(netConfig["ipv4.address"])
		if err != nil {
			return nil, nil, err
		}

		// Check the existing static DHCP IP is still valid in the subnet & ranges, if not
		// then we'll need to generate a new one.
		ranges := d.networkDHCPv4Ranges(netConfig)
		if d.networkDHCPValidIP(subnet, ranges, curIPv4.IP.To4()) {
			IPv4 = curIPv4.IP.To4()
		}
	}

	// If no static IPv6, then check if there is a valid static DHCP IPv6 address defined.
	if IPv6 == nil && curIPv6.IP != nil {
		_, subnet, err := net.ParseCIDR(netConfig["ipv6.address"])
		if err != nil {
			return IPv4, IPv6, err
		}

		// Check the existing static DHCP IP is still valid in the subnet & ranges, if not
		// then we'll need to generate a new one.
		ranges := d.networkDHCPv6Ranges(netConfig)
		if d.networkDHCPValidIP(subnet, ranges, curIPv6.IP.To16()) {
			IPv6 = curIPv6.IP.To16()
		}
	}

	// If we need to generate either a new IPv4 or IPv6, load existing IPs used in network.
	if IPv4 == nil || IPv6 == nil {
		// Get existing allocations in network.
		IPv4Allocs, IPv6Allocs, err := d.getDHCPAllocatedIPs(d.config["parent"])
		if err != nil {
			return nil, nil, err
		}

		// Allocate a new IPv4 address is IPv4 filtering enabled.
		if IPv4 == nil && shared.IsTrue(d.config["security.ipv4_filtering"]) {
			IPv4, err = d.getDHCPFreeIPv4(IPv4Allocs, netConfig, d.instance.Name(), d.config["hwaddr"])
			if err != nil {
				return nil, nil, err
			}
		}

		// Allocate a new IPv6 address is IPv6 filtering enabled.
		if IPv6 == nil && shared.IsTrue(d.config["security.ipv6_filtering"]) {
			IPv6, err = d.getDHCPFreeIPv6(IPv6Allocs, netConfig, d.instance.Name(), d.config["hwaddr"])
			if err != nil {
				return nil, nil, err
			}
		}
	}

	// If either IPv4 or IPv6 assigned is different than what is in dnsmasq config, rebuild config.
	if (IPv4 != nil && bytes.Compare(curIPv4.IP, IPv4.To4()) != 0) || (IPv6 != nil && bytes.Compare(curIPv6.IP, IPv6.To16()) != 0) {
		var IPv4Str, IPv6Str string

		if IPv4 != nil {
			IPv4Str = IPv4.String()
		}

		if IPv6 != nil {
			IPv6Str = IPv6.String()
		}

		err = dnsmasq.UpdateStaticEntry(d.config["parent"], d.instance.Project(), d.instance.Name(), netConfig, d.config["hwaddr"], IPv4Str, IPv6Str)
		if err != nil {
			return nil, nil, err
		}

		err = dnsmasq.Kill(d.config["parent"], true)
		if err != nil {
			return nil, nil, err
		}
	}

	return IPv4, IPv6, nil
}

// generateFilterIptablesRules returns a customised set of iptables filter rules based on the device.
func (d *nicBridged) generateFilterIptablesRules(m config.Device, IPv6 net.IP) (rules [][]string, err error) {
	mac, err := net.ParseMAC(m["hwaddr"])
	if err != nil {
		return
	}

	macHex := hex.EncodeToString(mac)

	// These rules below are implemented using ip6tables because the functionality to inspect
	// the contents of an ICMPv6 packet does not exist in ebtables (unlike for IPv4 ARP).
	// Additionally, ip6tables doesn't really provide a nice way to do what we need here, so we
	// have resorted to doing a raw hex comparison of the packet contents at fixed positions.
	// If these rules are not added then it is possible to hijack traffic for another IP that is
	// not assigned to the instance by sending a specially crafted gratuitous NDP packet with
	// correct source address and MAC at the IP & ethernet layers, but a fraudulent IP or MAC
	// inside the ICMPv6 NDP packet.
	if shared.IsTrue(m["security.ipv6_filtering"]) && IPv6 != nil {
		ipv6Hex := hex.EncodeToString(IPv6)

		rules = append(rules,
			// Prevent Neighbor Advertisement IP spoofing (prevents the instance redirecting traffic for IPs that are not its own).
			[]string{"ipv6", "INPUT", "-i", m["parent"], "-p", "ipv6-icmp", "-m", "physdev", "--physdev-in", m["host_name"], "-m", "icmp6", "--icmpv6-type", "136", "-m", "string", "!", "--hex-string", fmt.Sprintf("|%s|", ipv6Hex), "--algo", "bm", "--from", "48", "--to", "64", "-j", "DROP"},
			[]string{"ipv6", "FORWARD", "-i", m["parent"], "-p", "ipv6-icmp", "-m", "physdev", "--physdev-in", m["host_name"], "-m", "icmp6", "--icmpv6-type", "136", "-m", "string", "!", "--hex-string", fmt.Sprintf("|%s|", ipv6Hex), "--algo", "bm", "--from", "48", "--to", "64", "-j", "DROP"},
			// Prevent Neighbor Advertisement MAC spoofing (prevents the instance poisoning the NDP cache of its neighbours with a MAC address that isn't its own).
			[]string{"ipv6", "INPUT", "-i", m["parent"], "-p", "ipv6-icmp", "-m", "physdev", "--physdev-in", m["host_name"], "-m", "icmp6", "--icmpv6-type", "136", "-m", "string", "!", "--hex-string", fmt.Sprintf("|%s|", macHex), "--algo", "bm", "--from", "66", "--to", "72", "-j", "DROP"},
			[]string{"ipv6", "FORWARD", "-i", m["parent"], "-p", "ipv6-icmp", "-m", "physdev", "--physdev-in", m["host_name"], "-m", "icmp6", "--icmpv6-type", "136", "-m", "string", "!", "--hex-string", fmt.Sprintf("|%s|", macHex), "--algo", "bm", "--from", "66", "--to", "72", "-j", "DROP"},
		)
	}

	return
}

// networkDHCPv4Ranges returns a parsed set of DHCPv4 ranges for a particular network.
func (d *nicBridged) networkDHCPv4Ranges(netConfig map[string]string) []dhcpRange {
	dhcpRanges := make([]dhcpRange, 0)
	if netConfig["ipv4.dhcp.ranges"] != "" {
		for _, r := range strings.Split(netConfig["ipv4.dhcp.ranges"], ",") {
			parts := strings.SplitN(strings.TrimSpace(r), "-", 2)
			if len(parts) == 2 {
				startIP := net.ParseIP(parts[0])
				endIP := net.ParseIP(parts[1])
				dhcpRanges = append(dhcpRanges, dhcpRange{
					Start: startIP.To4(),
					End:   endIP.To4(),
				})
			}
		}
	}

	return dhcpRanges
}

// networkDHCPv6Ranges returns a parsed set of DHCPv6 ranges for a particular network.
func (d *nicBridged) networkDHCPv6Ranges(netConfig map[string]string) []dhcpRange {
	dhcpRanges := make([]dhcpRange, 0)
	if netConfig["ipv6.dhcp.ranges"] != "" {
		for _, r := range strings.Split(netConfig["ipv6.dhcp.ranges"], ",") {
			parts := strings.SplitN(strings.TrimSpace(r), "-", 2)
			if len(parts) == 2 {
				startIP := net.ParseIP(parts[0])
				endIP := net.ParseIP(parts[1])
				dhcpRanges = append(dhcpRanges, dhcpRange{
					Start: startIP.To16(),
					End:   endIP.To16(),
				})
			}
		}
	}

	return dhcpRanges
}

// networkDHCPValidIP returns whether an IP fits inside one of the supplied DHCP ranges and subnet.
func (d *nicBridged) networkDHCPValidIP(subnet *net.IPNet, ranges []dhcpRange, IP net.IP) bool {
	inSubnet := subnet.Contains(IP)
	if !inSubnet {
		return false
	}

	if len(ranges) > 0 {
		for _, IPRange := range ranges {
			if bytes.Compare(IP, IPRange.Start) >= 0 && bytes.Compare(IP, IPRange.End) <= 0 {
				return true
			}
		}
	} else if inSubnet {
		return true
	}

	return false
}

// getDHCPAllocatedIPs returns a map of IPs currently allocated (statically and dynamically)
// in dnsmasq for a specific network. The returned map is keyed by a 16 byte array representing
// the net.IP format. The value of each map item is a dhcpAllocation struct containing at least
// whether the allocation was static or dynamic and optionally instance name or MAC address.
// MAC addresses are only included for dynamic IPv4 allocations (where name is not reliable).
// Static allocations are not overridden by dynamic allocations, allowing for instance name to be
// included for static IPv6 allocations. IPv6 addresses that are dynamically assigned cannot be
// reliably linked to instances using either name or MAC because dnsmasq does not record the MAC
// address for these records, and the recorded host name can be set by the instance if the dns.mode
// for the network is set to "dynamic" and so cannot be trusted, so in this case we do not return
// any identifying info.
func (d *nicBridged) getDHCPAllocatedIPs(network string) (map[[4]byte]dhcpAllocation, map[[16]byte]dhcpAllocation, error) {
	IPv4s := make(map[[4]byte]dhcpAllocation)
	IPv6s := make(map[[16]byte]dhcpAllocation)

	// First read all statically allocated IPs.
	files, err := ioutil.ReadDir(shared.VarPath("networks", network, "dnsmasq.hosts"))
	if err != nil {
		return IPv4s, IPv6s, err
	}

	for _, entry := range files {
		IPv4, IPv6, err := d.getDHCPStaticIPs(network, entry.Name())
		if err != nil {
			return IPv4s, IPv6s, err
		}

		if IPv4.IP != nil {
			var IPKey [4]byte
			copy(IPKey[:], IPv4.IP.To4())
			IPv4s[IPKey] = IPv4
		}

		if IPv6.IP != nil {
			var IPKey [16]byte
			copy(IPKey[:], IPv6.IP.To16())
			IPv6s[IPKey] = IPv6
		}
	}

	// Next read all dynamic allocated IPs.
	file, err := os.Open(shared.VarPath("networks", network, "dnsmasq.leases"))
	if err != nil {
		return IPv4s, IPv6s, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 5 {
			IP := net.ParseIP(fields[2])
			if IP == nil {
				return IPv4s, IPv6s, fmt.Errorf("Error parsing IP address: %v", fields[2])
			}

			// Handle IPv6 addresses.
			if IP.To4() == nil {
				var IPKey [16]byte
				copy(IPKey[:], IP.To16())

				// Don't replace IPs from static config as more reliable.
				if IPv6s[IPKey].Name != "" {
					continue
				}

				IPv6s[IPKey] = dhcpAllocation{
					Static: false,
					IP:     IP.To16(),
				}
			} else {
				// MAC only available in IPv4 leases.
				MAC, err := net.ParseMAC(fields[1])
				if err != nil {
					return IPv4s, IPv6s, err
				}

				var IPKey [4]byte
				copy(IPKey[:], IP.To4())

				// Don't replace IPs from static config as more reliable.
				if IPv4s[IPKey].Name != "" {
					continue
				}

				IPv4s[IPKey] = dhcpAllocation{
					MAC:    MAC,
					Static: false,
					IP:     IP.To4(),
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return IPv4s, IPv6s, err
	}

	return IPv4s, IPv6s, nil
}

// getDHCPFreeIPv4 attempts to find a free IPv4 address for the device.
// It first checks whether there is an existing allocation for the instance.
// If no previous allocation, then a free IP is picked from the ranges configured.
func (d *nicBridged) getDHCPFreeIPv4(usedIPs map[[4]byte]dhcpAllocation, netConfig map[string]string, ctName string, deviceMAC string) (net.IP, error) {
	MAC, err := net.ParseMAC(deviceMAC)
	if err != nil {
		return nil, err
	}

	lxdIP, subnet, err := net.ParseCIDR(netConfig["ipv4.address"])
	if err != nil {
		return nil, err
	}

	dhcpRanges := d.networkDHCPv4Ranges(netConfig)

	// Lets see if there is already an allocation for our device and that it sits within subnet.
	// If there are custom DHCP ranges defined, check also that the IP falls within one of the ranges.
	for _, DHCP := range usedIPs {
		if (ctName == DHCP.Name || bytes.Compare(MAC, DHCP.MAC) == 0) && d.networkDHCPValidIP(subnet, dhcpRanges, DHCP.IP) {
			return DHCP.IP, nil
		}
	}

	// If no custom ranges defined, convert subnet pool to a range.
	if len(dhcpRanges) <= 0 {
		dhcpRanges = append(dhcpRanges, dhcpRange{Start: d.networkGetIP(subnet, 1).To4(), End: d.networkGetIP(subnet, -2).To4()})
	}

	// If no valid existing allocation found, try and find a free one in the subnet pool/ranges.
	for _, IPRange := range dhcpRanges {
		inc := big.NewInt(1)
		startBig := big.NewInt(0)
		startBig.SetBytes(IPRange.Start)
		endBig := big.NewInt(0)
		endBig.SetBytes(IPRange.End)

		for {
			if startBig.Cmp(endBig) >= 0 {
				break
			}

			IP := net.IP(startBig.Bytes())

			// Check IP generated is not LXD's IP.
			if IP.Equal(lxdIP) {
				startBig.Add(startBig, inc)
				continue
			}

			// Check IP is not already allocated.
			var IPKey [4]byte
			copy(IPKey[:], IP.To4())
			if _, inUse := usedIPs[IPKey]; inUse {
				startBig.Add(startBig, inc)
				continue
			}

			return IP, nil
		}
	}

	return nil, fmt.Errorf("No available IP could not be found")
}

// getDHCPFreeIPv6 attempts to find a free IPv6 address for the device.
// It first checks whether there is an existing allocation for the instance. Due to the limitations
// of dnsmasq lease file format, we can only search for previous static allocations.
// If no previous allocation, then if SLAAC (stateless) mode is enabled on the network, or if
// DHCPv6 stateful mode is enabled without custom ranges, then an EUI64 IP is generated from the
// device's MAC address. Finally if stateful custom ranges are enabled, then a free IP is picked
// from the ranges configured.
func (d *nicBridged) getDHCPFreeIPv6(usedIPs map[[16]byte]dhcpAllocation, netConfig map[string]string, ctName string, deviceMAC string) (net.IP, error) {
	lxdIP, subnet, err := net.ParseCIDR(netConfig["ipv6.address"])
	if err != nil {
		return nil, err
	}

	dhcpRanges := d.networkDHCPv6Ranges(netConfig)

	// Lets see if there is already an allocation for our device and that it sits within subnet.
	// Because of dnsmasq's lease file format we can only match safely against static
	// allocations using instance name. If there are custom DHCP ranges defined, check also
	// that the IP falls within one of the ranges.
	for _, DHCP := range usedIPs {
		if ctName == DHCP.Name && d.networkDHCPValidIP(subnet, dhcpRanges, DHCP.IP) {
			return DHCP.IP, nil
		}
	}

	// Try using an EUI64 IP when in either SLAAC or DHCPv6 stateful mode without custom ranges.
	if !shared.IsTrue(netConfig["ipv6.dhcp.stateful"]) || netConfig["ipv6.dhcp.ranges"] == "" {
		MAC, err := net.ParseMAC(deviceMAC)
		if err != nil {
			return nil, err
		}

		IP, err := eui64.ParseMAC(subnet.IP, MAC)
		if err != nil {
			return nil, err
		}

		// Check IP is not already allocated and not the LXD IP.
		var IPKey [16]byte
		copy(IPKey[:], IP.To16())
		_, inUse := usedIPs[IPKey]
		if !inUse && !IP.Equal(lxdIP) {
			return IP, nil
		}
	}

	// If no custom ranges defined, convert subnet pool to a range.
	if len(dhcpRanges) <= 0 {
		dhcpRanges = append(dhcpRanges, dhcpRange{Start: d.networkGetIP(subnet, 1).To16(), End: d.networkGetIP(subnet, -1).To16()})
	}

	// If we get here, then someone already has our SLAAC IP, or we are using custom ranges.
	// Try and find a free one in the subnet pool/ranges.
	for _, IPRange := range dhcpRanges {
		inc := big.NewInt(1)
		startBig := big.NewInt(0)
		startBig.SetBytes(IPRange.Start)
		endBig := big.NewInt(0)
		endBig.SetBytes(IPRange.End)

		for {
			if startBig.Cmp(endBig) >= 0 {
				break
			}

			IP := net.IP(startBig.Bytes())

			// Check IP generated is not LXD's IP.
			if IP.Equal(lxdIP) {
				startBig.Add(startBig, inc)
				continue
			}

			// Check IP is not already allocated.
			var IPKey [16]byte
			copy(IPKey[:], IP.To16())
			if _, inUse := usedIPs[IPKey]; inUse {
				startBig.Add(startBig, inc)
				continue
			}

			return IP, nil
		}
	}

	return nil, fmt.Errorf("No available IP could not be found")
}

func (d *nicBridged) networkGetIP(subnet *net.IPNet, host int64) net.IP {
	// Convert IP to a big int
	bigIP := big.NewInt(0)
	bigIP.SetBytes(subnet.IP.To16())

	// Deal with negative offsets
	bigHost := big.NewInt(host)
	bigCount := big.NewInt(host)
	if host < 0 {
		mask, size := subnet.Mask.Size()

		bigHosts := big.NewFloat(0)
		bigHosts.SetFloat64((math.Pow(2, float64(size-mask))))
		bigHostsInt, _ := bigHosts.Int(nil)

		bigCount.Set(bigHostsInt)
		bigCount.Add(bigCount, bigHost)
	}

	// Get the new IP int
	bigIP.Add(bigIP, bigCount)

	// Generate an IPv6
	if subnet.IP.To4() == nil {
		newIP := bigIP.Bytes()
		return newIP
	}

	// Generate an IPv4
	newIP := make(net.IP, 4)
	binary.BigEndian.PutUint32(newIP, uint32(bigIP.Int64()))
	return newIP
}

const (
	clearLeaseAll = iota
	clearLeaseIPv4Only
	clearLeaseIPv6Only
)

// networkClearLease clears leases from a running dnsmasq process.
func (d *nicBridged) networkClearLease(name string, network string, hwaddr string, mode int) error {
	leaseFile := shared.VarPath("networks", network, "dnsmasq.leases")

	// Check that we are in fact running a dnsmasq for the network
	if !shared.PathExists(leaseFile) {
		return nil
	}

	// Convert MAC string to bytes to avoid any case comparison issues later.
	srcMAC, err := net.ParseMAC(hwaddr)
	if err != nil {
		return err
	}

	iface, err := net.InterfaceByName(network)
	if err != nil {
		return err
	}

	// Get IPv4 and IPv6 address of interface running dnsmasq on host.
	addrs, err := iface.Addrs()
	if err != nil {
		return err
	}

	var dstIPv4, dstIPv6 net.IP
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			return err
		}
		if !ip.IsGlobalUnicast() {
			continue
		}
		if ip.To4() == nil {
			dstIPv6 = ip
		} else {
			dstIPv4 = ip
		}
	}

	// Iterate the dnsmasq leases file looking for matching leases for this instance to release.
	file, err := os.Open(leaseFile)
	if err != nil {
		return err
	}
	defer file.Close()

	var dstDUID string
	errs := []error{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		fieldsLen := len(fields)

		// Handle lease lines
		if fieldsLen == 5 {
			if (mode == clearLeaseAll || mode == clearLeaseIPv4Only) && srcMAC.String() == fields[1] { // Handle IPv4 leases by matching MAC address to lease.
				srcIP := net.ParseIP(fields[2])

				if dstIPv4 == nil {
					errs = append(errs, fmt.Errorf("Failed to release DHCPv4 lease for instance \"%s\", IP \"%s\", MAC \"%s\", %v", name, srcIP, srcMAC, "No server address found"))
					continue
				}

				err = d.networkDHCPv4Release(srcMAC, srcIP, dstIPv4)
				if err != nil {
					errs = append(errs, fmt.Errorf("Failed to release DHCPv4 lease for instance \"%s\", IP \"%s\", MAC \"%s\", %v", name, srcIP, srcMAC, err))
				}
			} else if (mode == clearLeaseAll || mode == clearLeaseIPv6Only) && name == fields[3] { // Handle IPv6 addresses by matching hostname to lease.
				IAID := fields[1]
				srcIP := net.ParseIP(fields[2])
				DUID := fields[4]

				// Skip IPv4 addresses.
				if srcIP.To4() != nil {
					continue
				}

				if dstIPv6 == nil {
					errs = append(errs, fmt.Errorf("Failed to release DHCPv6 lease for instance \"%s\", IP \"%s\", DUID \"%s\", IAID \"%s\": %s", name, srcIP, DUID, IAID, "No server address found"))
					continue // Cant send release packet if no dstIP found.
				}

				if dstDUID == "" {
					errs = append(errs, fmt.Errorf("Failed to release DHCPv6 lease for instance \"%s\", IP \"%s\", DUID \"%s\", IAID \"%s\": %s", name, srcIP, DUID, IAID, "No server DUID found"))
					continue // Cant send release packet if no dstDUID found.
				}

				err = d.networkDHCPv6Release(DUID, IAID, srcIP, dstIPv6, dstDUID)
				if err != nil {
					errs = append(errs, fmt.Errorf("Failed to release DHCPv6 lease for instance \"%s\", IP \"%s\", DUID \"%s\", IAID \"%s\": %v", name, srcIP, DUID, IAID, err))
				}
			}
		} else if fieldsLen == 2 && fields[0] == "duid" {
			// Handle server DUID line needed for releasing IPv6 leases.
			// This should come before the IPv6 leases in the lease file.
			dstDUID = fields[1]
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%v", errs)
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

// networkDHCPv4Release sends a DHCPv4 release packet to a DHCP server.
func (d *nicBridged) networkDHCPv4Release(srcMAC net.HardwareAddr, srcIP net.IP, dstIP net.IP) error {
	dstAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:67", dstIP.String()))
	if err != nil {
		return err
	}
	conn, err := net.DialUDP("udp", nil, dstAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	//Random DHCP transaction ID
	xid := rand.Uint32()

	// Construct a DHCP packet pretending to be from the source IP and MAC supplied.
	dhcp := layers.DHCPv4{
		Operation:    layers.DHCPOpRequest,
		HardwareType: layers.LinkTypeEthernet,
		ClientHWAddr: srcMAC,
		ClientIP:     srcIP,
		Xid:          xid,
	}

	// Add options to DHCP release packet.
	dhcp.Options = append(dhcp.Options,
		layers.NewDHCPOption(layers.DHCPOptMessageType, []byte{byte(layers.DHCPMsgTypeRelease)}),
		layers.NewDHCPOption(layers.DHCPOptServerID, dstIP.To4()),
	)

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}

	err = gopacket.SerializeLayers(buf, opts, &dhcp)
	if err != nil {
		return err
	}

	_, err = conn.Write(buf.Bytes())
	return err
}

// networkDHCPv6Release sends a DHCPv6 release packet to a DHCP server.
func (d *nicBridged) networkDHCPv6Release(srcDUID string, srcIAID string, srcIP net.IP, dstIP net.IP, dstDUID string) error {
	dstAddr, err := net.ResolveUDPAddr("udp6", fmt.Sprintf("[%s]:547", dstIP.String()))
	if err != nil {
		return err
	}
	conn, err := net.DialUDP("udp6", nil, dstAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Construct a DHCPv6 packet pretending to be from the source IP and MAC supplied.
	dhcp := layers.DHCPv6{
		MsgType: layers.DHCPv6MsgTypeRelease,
	}

	// Convert Server DUID from string to byte array
	dstDUIDRaw, err := hex.DecodeString(strings.Replace(dstDUID, ":", "", -1))
	if err != nil {
		return err
	}

	// Convert DUID from string to byte array
	srcDUIDRaw, err := hex.DecodeString(strings.Replace(srcDUID, ":", "", -1))
	if err != nil {
		return err
	}

	// Convert IAID string to int
	srcIAIDRaw, err := strconv.ParseUint(srcIAID, 10, 32)
	if err != nil {
		return err
	}
	srcIAIDRaw32 := uint32(srcIAIDRaw)

	// Build the Identity Association details option manually (as not provided by gopacket).
	iaAddr := d.networkDHCPv6CreateIAAddress(srcIP)
	ianaRaw := d.networkDHCPv6CreateIANA(srcIAIDRaw32, iaAddr)

	// Add options to DHCP release packet.
	dhcp.Options = append(dhcp.Options,
		layers.NewDHCPv6Option(layers.DHCPv6OptServerID, dstDUIDRaw),
		layers.NewDHCPv6Option(layers.DHCPv6OptClientID, srcDUIDRaw),
		layers.NewDHCPv6Option(layers.DHCPv6OptIANA, ianaRaw),
	)

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}

	err = gopacket.SerializeLayers(buf, opts, &dhcp)
	if err != nil {
		return err
	}

	_, err = conn.Write(buf.Bytes())
	return err
}

// networkDHCPv6CreateIANA creates a DHCPv6 Identity Association for Non-temporary Address (rfc3315 IA_NA) option.
func (d *nicBridged) networkDHCPv6CreateIANA(IAID uint32, IAAddr []byte) []byte {
	data := make([]byte, 12)
	binary.BigEndian.PutUint32(data[0:4], IAID)       // Identity Association Identifier
	binary.BigEndian.PutUint32(data[4:8], uint32(0))  // T1
	binary.BigEndian.PutUint32(data[8:12], uint32(0)) // T2
	data = append(data, IAAddr...)                    // Append the IA Address details
	return data
}

// networkDHCPv6CreateIAAddress creates a DHCPv6 Identity Association Address (rfc3315) option.
func (d *nicBridged) networkDHCPv6CreateIAAddress(IP net.IP) []byte {
	data := make([]byte, 28)
	binary.BigEndian.PutUint16(data[0:2], uint16(layers.DHCPv6OptIAAddr)) // Sub-Option type
	binary.BigEndian.PutUint16(data[2:4], uint16(24))                     // Length (fixed at 24 bytes)
	copy(data[4:20], IP)                                                  // IPv6 address to be released
	binary.BigEndian.PutUint32(data[20:24], uint32(0))                    // Preferred liftetime
	binary.BigEndian.PutUint32(data[24:28], uint32(0))                    // Valid lifetime
	return data
}
