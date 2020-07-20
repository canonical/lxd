package device

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/mdlayher/netx/eui64"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/dnsmasq"
	firewallDrivers "github.com/lxc/lxd/lxd/firewall/drivers"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

type nicBridged struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *nicBridged) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	var requiredFields []string
	optionalFields := []string{
		"name",
		"network",
		"parent",
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
		"boot.priority",
		"vlan",
	}

	// Check that if network proeperty is set that conflicting keys are not present.
	if d.config["network"] != "" {
		requiredFields = append(requiredFields, "network")

		bannedKeys := []string{"nictype", "parent", "mtu", "maas.subnet.ipv4", "maas.subnet.ipv6"}
		for _, bannedKey := range bannedKeys {
			if d.config[bannedKey] != "" {
				return fmt.Errorf("Cannot use %q property in conjunction with %q property", bannedKey, "network")
			}
		}

		// If network property is specified, lookup network settings and apply them to the device's config.
		n, err := network.LoadByName(d.state, d.config["network"])
		if err != nil {
			return errors.Wrapf(err, "Error loading network config for %q", d.config["network"])
		}

		if n.Status() == api.NetworkStatusPending {
			return fmt.Errorf("Specified network is not fully created")
		}

		if n.Type() != "bridge" {
			return fmt.Errorf("Specified network must be of type bridge")
		}

		netConfig := n.Config()

		if d.config["ipv4.address"] != "" {
			// Check that DHCPv4 is enabled on parent network (needed to use static assigned IPs).
			if !n.HasDHCPv4() {
				return fmt.Errorf("Cannot specify %q when %q is disabled on network %q", "ipv4.address", "ipv4.dhcp", d.config["network"])
			}

			_, subnet, err := net.ParseCIDR(netConfig["ipv4.address"])
			if err != nil {
				return errors.Wrapf(err, "Invalid network ipv4.address")
			}

			// Check the static IP supplied is valid for the linked network. It should be part of the
			// network's subnet, but not necessarily part of the dynamic allocation ranges.
			if !d.networkDHCPValidIP(subnet, nil, net.ParseIP(d.config["ipv4.address"])) {
				return fmt.Errorf("Device IP address %q not within network %q subnet", d.config["ipv4.address"], d.config["network"])
			}
		}

		if d.config["ipv6.address"] != "" {
			// Check that DHCPv6 is enabled on parent network (needed to use static assigned IPs).
			if !n.HasDHCPv6() || !shared.IsTrue(netConfig["ipv6.dhcp.stateful"]) {
				return fmt.Errorf("Cannot specify %q when %q or %q are disabled on network %q", "ipv6.address", "ipv6.dhcp", "ipv6.dhcp.stateful", d.config["network"])
			}

			_, subnet, err := net.ParseCIDR(netConfig["ipv6.address"])
			if err != nil {
				return errors.Wrapf(err, "Invalid network ipv6.address")
			}

			// Check the static IP supplied is valid for the linked network. It should be part of the
			// network's subnet, but not necessarily part of the dynamic allocation ranges.
			if !d.networkDHCPValidIP(subnet, nil, net.ParseIP(d.config["ipv6.address"])) {
				return fmt.Errorf("Device IP address %q not within network %q subnet", d.config["ipv6.address"], d.config["network"])
			}
		}

		// Link device to network bridge.
		d.config["parent"] = d.config["network"]

		// Apply network level config options to device config before validation.
		if netConfig["bridge.mtu"] != "" {
			d.config["mtu"] = netConfig["bridge.mtu"]
		}

		// Copy certain keys verbatim from the network's settings.
		inheritKeys := []string{"maas.subnet.ipv4", "maas.subnet.ipv6"}
		for _, inheritKey := range inheritKeys {
			if _, found := netConfig[inheritKey]; found {
				d.config[inheritKey] = netConfig[inheritKey]
			}
		}
	} else {
		// If no network property supplied, then parent property is required.
		requiredFields = append(requiredFields, "parent")
	}

	// Check that IP filtering isn't being used with VLAN filtering.
	if shared.IsTrue(d.config["security.ipv4_filtering"]) || shared.IsTrue(d.config["security.ipv6_filtering"]) {
		if d.config["vlan"] != "" || d.config["vlan.tagged"] != "" {
			return fmt.Errorf("IP filtering cannot be used with VLAN filtering")
		}
	}

	rules := nicValidationRules(requiredFields, optionalFields)

	// Add bridge specific vlan validation.
	rules["vlan"] = func(value string) error {
		if value == "" || value == "none" {
			return nil
		}

		return networkValidVLAN(value)
	}

	// Add bridge specific vlan.tagged validation.
	rules["vlan.tagged"] = func(value string) error {
		if value == "" {
			return nil
		}

		err := networkValidVLANList(value)
		if err != nil {
			return err
		}

		// Check that none of the supplied VLAN IDs are the same as the untagged VLAN ID.
		for _, vlanID := range strings.Split(value, ",") {
			vlanID = strings.TrimSpace(vlanID)
			if vlanID == d.config["vlan"] {
				return fmt.Errorf("Tagged VLAN ID %q cannot be the same as untagged VLAN ID", vlanID)
			}
		}

		return nil
	}

	// Now run normal validation.
	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicBridged) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
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

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicBridged) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	revert := revert.New()
	defer revert.Fail()

	saveData := make(map[string]string)
	saveData["host_name"] = d.config["host_name"]

	var peerName string

	// Create veth pair and configure the peer end with custom hwaddr and mtu if supplied.
	if d.inst.Type() == instancetype.Container {
		if saveData["host_name"] == "" {
			saveData["host_name"] = networkRandomDevName("veth")
		}
		peerName, err = networkCreateVethPair(saveData["host_name"], d.config)
	} else if d.inst.Type() == instancetype.VM {
		if saveData["host_name"] == "" {
			saveData["host_name"] = networkRandomDevName("tap")
		}
		peerName = saveData["host_name"] // VMs use the host_name to link to the TAP FD.
		err = networkCreateTap(saveData["host_name"], d.config)
	}

	if err != nil {
		return nil, err
	}

	revert.Add(func() { NetworkRemoveInterface(saveData["host_name"]) })

	// Apply and host-side limits and routes.
	err = networkSetupHostVethDevice(d.config, nil, saveData)
	if err != nil {
		return nil, err
	}

	// Disable IPv6 on host-side veth interface (prevents host-side interface getting link-local address)
	// which isn't needed because the host-side interface is connected to a bridge.
	err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/disable_ipv6", saveData["host_name"]), "1")
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Apply and host-side network filters (uses enriched host_name from networkSetupHostVethDevice).
	err = d.setupHostFilters(nil)
	if err != nil {
		return nil, err
	}
	revert.Add(func() { d.removeFilters(d.config) })

	// Attach host side veth interface to bridge.
	err = network.AttachInterface(d.config["parent"], saveData["host_name"])
	if err != nil {
		return nil, err
	}
	revert.Add(func() { network.DetachInterface(d.config["parent"], saveData["host_name"]) })

	// Attempt to disable router advertisement acceptance.
	err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/accept_ra", saveData["host_name"]), "0")
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Detech bridge type and setup VLAN settings on bridge port.
	if network.IsNativeBridge(d.config["parent"]) {
		err = d.setupNativeBridgePortVLANs(saveData["host_name"])
	} else {
		err = d.setupOVSBridgePortVLANs(saveData["host_name"])
	}

	if err != nil {
		return nil, err
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "name", Value: d.config["name"]},
		{Key: "type", Value: "phys"},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: peerName},
	}

	if d.inst.Type() == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface,
			[]deviceConfig.RunConfigItem{
				{Key: "devName", Value: d.name},
				{Key: "hwaddr", Value: d.config["hwaddr"]},
			}...)
	}

	revert.Success()
	return &runConf, nil
}

// Update applies configuration changes to a started device.
func (d *nicBridged) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	oldConfig := oldDevices[d.name]

	// If an IPv6 address has changed, flush all existing IPv6 leases for instance so instance
	// isn't allocated old IP. This is important with IPv6 because DHCPv6 supports multiple IP
	// address allocation and would result in instance having leases for both old and new IPs.
	if d.config["hwaddr"] != "" && d.config["ipv6.address"] != oldConfig["ipv6.address"] {
		err := d.networkClearLease(d.inst.Name(), d.config["parent"], d.config["hwaddr"], clearLeaseIPv6Only)
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
func (d *nicBridged) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicBridged) postStop() error {
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
		// Detach host-side end of veth pair from bridge (required for openvswitch particularly).
		err := network.DetachInterface(d.config["parent"], d.config["host_name"])
		if err != nil {
			return errors.Wrapf(err, "Failed to detach interface %q from %q", d.config["host_name"], d.config["parent"])
		}

		// Removing host-side end of veth pair will delete the peer end too.
		err = NetworkRemoveInterface(d.config["host_name"])
		if err != nil {
			return errors.Wrapf(err, "Failed to remove interface %q", d.config["host_name"])
		}
	}

	networkRemoveVethRoutes(d.config)
	d.removeFilters(d.config)

	return nil
}

// Remove is run when the device is removed from the instance or the instance is deleted.
func (d *nicBridged) Remove() error {
	err := d.networkClearLease(d.inst.Name(), d.config["parent"], d.config["hwaddr"], clearLeaseAll)
	if err != nil {
		return err
	}

	if d.config["parent"] != "" {
		dnsmasq.ConfigMutex.Lock()
		defer dnsmasq.ConfigMutex.Unlock()

		// Remove dnsmasq config if it exists (doesn't return error if file is missing).
		err := dnsmasq.RemoveStaticEntry(d.config["parent"], d.inst.Project(), d.inst.Name())
		if err != nil {
			return err
		}

		// Reload dnsmasq to apply new settings if dnsmasq is running.
		if shared.PathExists(shared.VarPath("networks", d.config["parent"], "dnsmasq.pid")) {
			err = dnsmasq.Kill(d.config["parent"], true)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// rebuildDnsmasqEntry rebuilds the dnsmasq host entry if connected to an LXD managed network and reloads dnsmasq.
func (d *nicBridged) rebuildDnsmasqEntry() error {
	// Rebuild dnsmasq config if a bridged device has changed and parent is a managed network.
	if !shared.PathExists(shared.VarPath("networks", d.config["parent"], "dnsmasq.pid")) {
		return nil
	}

	dnsmasq.ConfigMutex.Lock()
	defer dnsmasq.ConfigMutex.Unlock()

	_, dbInfo, err := d.state.Cluster.GetNetworkInAnyState(d.config["parent"])
	if err != nil {
		return err
	}

	netConfig := dbInfo.Config
	ipv4Address := d.config["ipv4.address"]
	ipv6Address := d.config["ipv6.address"]

	// If IP filtering is enabled, and no static IP in config, check if there is already a
	// dynamically assigned static IP in dnsmasq config and write that back out in new config.
	if (shared.IsTrue(d.config["security.ipv4_filtering"]) && ipv4Address == "") || (shared.IsTrue(d.config["security.ipv6_filtering"]) && ipv6Address == "") {
		curIPv4, curIPv6, err := dnsmasq.DHCPStaticIPs(d.config["parent"], d.inst.Project(), d.inst.Name())
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

	err = dnsmasq.UpdateStaticEntry(d.config["parent"], d.inst.Project(), d.inst.Name(), netConfig, d.config["hwaddr"], ipv4Address, ipv6Address)
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
func (d *nicBridged) setupHostFilters(oldConfig deviceConfig.Device) error {
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
func (d *nicBridged) removeFilters(m deviceConfig.Device) {
	if m["hwaddr"] == "" {
		logger.Errorf("Failed to remove network filters for %q: hwaddr not defined", d.name)
		return
	}

	if m["host_name"] == "" {
		logger.Errorf("Failed to remove network filters for %q: host_name not defined", d.name)
		return
	}

	var IPv4, IPv6 net.IP

	if m["ipv4.address"] != "" {
		IPv4 = net.ParseIP(m["ipv4.address"])
	}

	if m["ipv6.address"] != "" {
		IPv6 = net.ParseIP(m["ipv6.address"])
	}

	// If no static IPv4 assigned, try removing the filter all rule in case it was setup.
	if IPv4 == nil {
		IPv4 = net.ParseIP(firewallDrivers.FilterIPv4All)
	}

	// If no static IPv6 assigned, try removing the filter all rule in case it was setup.
	if IPv6 == nil {
		IPv6 = net.ParseIP(firewallDrivers.FilterIPv6All)
	}

	// Remove filters for static MAC and IPs (if specified above).
	// This covers the case when filtering is used with an unmanaged bridge.
	logger.Debug("Clearing instance firewall static filters", log.Ctx{"project": d.inst.Project(), "instance": d.inst.Name(), "parent": m["parent"], "dev": d.name, "host_name": m["host_name"], "hwaddr": m["hwaddr"], "ipv4": IPv4, "ipv6": IPv6})
	err := d.state.Firewall.InstanceClearBridgeFilter(d.inst.Project(), d.inst.Name(), d.name, m["parent"], m["host_name"], m["hwaddr"], IPv4, IPv6)
	if err != nil {
		logger.Errorf("Failed to remove static IP network filters for %q: %v", d.name, err)
	}

	// Read current static DHCP IP allocation configured from dnsmasq host config (if exists).
	// This covers the case when IPs are not defined in config, but have been assigned in managed DHCP.
	IPv4Alloc, IPv6Alloc, err := dnsmasq.DHCPStaticIPs(m["parent"], d.inst.Project(), d.inst.Name())
	if err != nil {
		if os.IsNotExist(err) {
			return
		}

		logger.Errorf("Failed to get static IP allocations for filter removal from %q: %v", d.name, err)
		return
	}

	logger.Debug("Clearing instance firewall dynamic filters", log.Ctx{"project": d.inst.Project(), "instance": d.inst.Name(), "parent": m["parent"], "dev": d.name, "host_name": m["host_name"], "hwaddr": m["hwaddr"], "ipv4": IPv4Alloc.IP, "ipv6": IPv6Alloc.IP})
	err = d.state.Firewall.InstanceClearBridgeFilter(d.inst.Project(), d.inst.Name(), d.name, m["parent"], m["host_name"], m["hwaddr"], IPv4Alloc.IP, IPv6Alloc.IP)
	if err != nil {
		logger.Errorf("Failed to remove DHCP network assigned filters  for %q: %v", d.name, err)
	}
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
		// Check br_netfilter kernel module is loaded and enabled for IPv6. We won't try to load it as its
		// default mode can cause unwanted traffic blocking.
		sysctlPath := "net/bridge/bridge-nf-call-ip6tables"
		sysctlVal, err := util.SysctlGet(sysctlPath)
		if err != nil {
			return errors.Wrapf(err, "security.ipv6_filtering requires br_netfilter be loaded")
		}

		if sysctlVal != "1\n" {
			return fmt.Errorf("security.ipv6_filtering requires br_netfilter sysctl net.bridge.bridge-nf-call-ip6tables=1")
		}
	}

	// Check if the parent is managed and load config. If parent is unmanaged continue anyway.
	var IPv4, IPv6 net.IP
	n, err := network.LoadByName(d.state, d.config["parent"])
	if err != nil && err != db.ErrNoSuchObject {
		return err
	}

	// If parent bridge is unmanaged check that IP filtering isn't enabled.
	if err == db.ErrNoSuchObject || n == nil {
		if shared.IsTrue(d.config["security.ipv4_filtering"]) || shared.IsTrue(d.config["security.ipv6_filtering"]) {
			return fmt.Errorf("IP filtering requires using a managed parent bridge")
		}
	}

	// If parent bridge is unmanaged we cannot allocate static IPs.
	if n != nil {
		// Retrieve existing IPs, or allocate new ones if needed.
		IPv4, IPv6, err = d.allocateFilterIPs(n)
		if err != nil {
			return err
		}
	}

	// If anything goes wrong, clean up so we don't leave orphaned rules.
	defer func() {
		if err != nil {
			d.removeFilters(d.config)
		}
	}()

	// If no allocated IPv4 address for filtering and filtering enabled, then block all IPv4 traffic.
	if shared.IsTrue(d.config["security.ipv4_filtering"]) && IPv4 == nil {
		IPv4 = net.ParseIP(firewallDrivers.FilterIPv4All)
	}

	// If no allocated IPv6 address for filtering and filtering enabled, then block all IPv6 traffic.
	if shared.IsTrue(d.config["security.ipv6_filtering"]) && IPv6 == nil {
		IPv6 = net.ParseIP(firewallDrivers.FilterIPv6All)
	}

	return d.state.Firewall.InstanceSetupBridgeFilter(d.inst.Project(), d.inst.Name(), d.name, d.config["parent"], d.config["host_name"], d.config["hwaddr"], IPv4, IPv6)
}

// networkAllocateVethFilterIPs retrieves previously allocated IPs, or allocate new ones if needed.
// This function only works with LXD managed networks, and as such, requires the managed network's
// config to be supplied.
func (d *nicBridged) allocateFilterIPs(n network.Network) (net.IP, net.IP, error) {
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

	netConfig := n.Config()

	// Check the conditions required to dynamically allocated IPs.
	canIPv4Allocate := netConfig["ipv4.address"] != "" && netConfig["ipv4.address"] != "none" && n.HasDHCPv4()
	canIPv6Allocate := netConfig["ipv6.address"] != "" && netConfig["ipv6.address"] != "none" && n.HasDHCPv6()

	dnsmasq.ConfigMutex.Lock()
	defer dnsmasq.ConfigMutex.Unlock()

	// Read current static IP allocation configured from dnsmasq host config (if exists).
	curIPv4, curIPv6, err := dnsmasq.DHCPStaticIPs(d.config["parent"], d.inst.Project(), d.inst.Name())
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}

	// If no static IPv4, then check if there is a valid static DHCP IPv4 address defined.
	if IPv4 == nil && curIPv4.IP != nil && canIPv4Allocate {
		_, subnet, err := net.ParseCIDR(netConfig["ipv4.address"])
		if err != nil {
			return nil, nil, err
		}

		// Check the existing static DHCP IP is still valid in the subnet & ranges, if not
		// then we'll need to generate a new one.
		ranges := n.DHCPv4Ranges()
		if d.networkDHCPValidIP(subnet, ranges, curIPv4.IP.To4()) {
			IPv4 = curIPv4.IP.To4()
		}
	}

	// If no static IPv6, then check if there is a valid static DHCP IPv6 address defined.
	if IPv6 == nil && curIPv6.IP != nil && canIPv6Allocate {
		_, subnet, err := net.ParseCIDR(netConfig["ipv6.address"])
		if err != nil {
			return IPv4, IPv6, err
		}

		// Check the existing static DHCP IP is still valid in the subnet & ranges, if not
		// then we'll need to generate a new one.
		ranges := n.DHCPv6Ranges()
		if d.networkDHCPValidIP(subnet, ranges, curIPv6.IP.To16()) {
			IPv6 = curIPv6.IP.To16()
		}
	}

	// If we need to generate either a new IPv4 or IPv6, load existing IPs used in network.
	if (IPv4 == nil && canIPv4Allocate) || (IPv6 == nil && canIPv6Allocate) {
		// Get existing allocations in network.
		IPv4Allocs, IPv6Allocs, err := dnsmasq.DHCPAllocatedIPs(d.config["parent"])
		if err != nil {
			return nil, nil, err
		}

		// Allocate a new IPv4 address if IPv4 filtering enabled.
		if IPv4 == nil && canIPv4Allocate && shared.IsTrue(d.config["security.ipv4_filtering"]) {
			IPv4, err = d.getDHCPFreeIPv4(IPv4Allocs, n, d.inst.Name(), d.config["hwaddr"])
			if err != nil {
				return nil, nil, err
			}
		}

		// Allocate a new IPv6 address if IPv6 filtering enabled.
		if IPv6 == nil && canIPv6Allocate && shared.IsTrue(d.config["security.ipv6_filtering"]) {
			IPv6, err = d.getDHCPFreeIPv6(IPv6Allocs, n, d.inst.Name(), d.config["hwaddr"])
			if err != nil {
				return nil, nil, err
			}
		}
	}

	// If parent is a DHCP enabled managed network and either IPv4 or IPv6 assigned is different than what is in dnsmasq config, rebuild config.
	if shared.PathExists(shared.VarPath("networks", d.config["parent"], "dnsmasq.pid")) &&
		((IPv4 != nil && bytes.Compare(curIPv4.IP, IPv4.To4()) != 0) || (IPv6 != nil && bytes.Compare(curIPv6.IP, IPv6.To16()) != 0)) {
		var IPv4Str, IPv6Str string

		if IPv4 != nil {
			IPv4Str = IPv4.String()
		}

		if IPv6 != nil {
			IPv6Str = IPv6.String()
		}

		err = dnsmasq.UpdateStaticEntry(d.config["parent"], d.inst.Project(), d.inst.Name(), netConfig, d.config["hwaddr"], IPv4Str, IPv6Str)
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

// networkDHCPValidIP returns whether an IP fits inside one of the supplied DHCP ranges and subnet.
func (d *nicBridged) networkDHCPValidIP(subnet *net.IPNet, ranges []network.DHCPRange, IP net.IP) bool {
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

// getDHCPFreeIPv4 attempts to find a free IPv4 address for the device.
// It first checks whether there is an existing allocation for the instance.
// If no previous allocation, then a free IP is picked from the ranges configured.
func (d *nicBridged) getDHCPFreeIPv4(usedIPs map[[4]byte]dnsmasq.DHCPAllocation, n network.Network, ctName string, deviceMAC string) (net.IP, error) {
	MAC, err := net.ParseMAC(deviceMAC)
	if err != nil {
		return nil, err
	}

	lxdIP, subnet, err := net.ParseCIDR(n.Config()["ipv4.address"])
	if err != nil {
		return nil, err
	}

	dhcpRanges := n.DHCPv4Ranges()

	// Lets see if there is already an allocation for our device and that it sits within subnet.
	// If there are custom DHCP ranges defined, check also that the IP falls within one of the ranges.
	for _, DHCP := range usedIPs {
		if (ctName == DHCP.Name || bytes.Compare(MAC, DHCP.MAC) == 0) && d.networkDHCPValidIP(subnet, dhcpRanges, DHCP.IP) {
			return DHCP.IP, nil
		}
	}

	// If no custom ranges defined, convert subnet pool to a range.
	if len(dhcpRanges) <= 0 {
		dhcpRanges = append(dhcpRanges, network.DHCPRange{
			Start: network.GetIP(subnet, 1).To4(),
			End:   network.GetIP(subnet, -2).To4()},
		)
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

			_, inUse := usedIPs[IPKey]
			if inUse {
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
func (d *nicBridged) getDHCPFreeIPv6(usedIPs map[[16]byte]dnsmasq.DHCPAllocation, n network.Network, ctName string, deviceMAC string) (net.IP, error) {
	netConfig := n.Config()
	lxdIP, subnet, err := net.ParseCIDR(netConfig["ipv6.address"])
	if err != nil {
		return nil, err
	}

	dhcpRanges := n.DHCPv6Ranges()

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
		dhcpRanges = append(dhcpRanges, network.DHCPRange{
			Start: network.GetIP(subnet, 1).To16(),
			End:   network.GetIP(subnet, -1).To16()},
		)
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

			_, inUse := usedIPs[IPKey]
			if inUse {
				startBig.Add(startBig, inc)
				continue
			}

			return IP, nil
		}
	}

	return nil, fmt.Errorf("No available IP could not be found")
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
					logger.Warnf("Failed to release DHCPv4 lease for instance \"%s\", IP \"%s\", MAC \"%s\", %v", name, srcIP, srcMAC, "No server address found")
					continue // Cant send release packet if no dstIP found.
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
					logger.Warn("Failed to release DHCPv6 lease for instance \"%s\", IP \"%s\", DUID \"%s\", IAID \"%s\": %s", name, srcIP, DUID, IAID, "No server address found")
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

	err = scanner.Err()
	if err != nil {
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

// setupNativeBridgePortVLANs configures the bridge port with the specified VLAN settings on the native bridge.
func (d *nicBridged) setupNativeBridgePortVLANs(hostName string) error {
	// Check vlan_filtering is enabled on bridge if needed.
	if d.config["vlan"] != "" || d.config["vlan.tagged"] != "" {
		vlanFilteringStatus, err := network.BridgeVLANFilteringStatus(d.config["parent"])
		if err != nil {
			return err
		}

		if vlanFilteringStatus != "1" {
			return fmt.Errorf("VLAN filtering is not enabled in parent bridge %q", d.config["parent"])
		}
	}

	// Set port on bridge to specified untagged PVID.
	if d.config["vlan"] != "" {
		// Reject VLAN ID 0 if specified (as main validation allows VLAN ID 0 to accommodate ovs).
		if d.config["vlan"] == "0" {
			return fmt.Errorf("VLAN ID 0 is not allowed for native Linux bridges")
		}

		// Get default PVID membership on port.
		defaultPVID, err := network.BridgeVLANDefaultPVID(d.config["parent"])
		if err != nil {
			return err
		}

		// If the default is different to the specified untagged VLAN or if tagged VLAN is set to "none"
		// then remove the default untagged membership.
		if defaultPVID != d.config["vlan"] || d.config["vlan"] == "none" {
			_, err = shared.RunCommand("bridge", "vlan", "del", "dev", hostName, "vid", defaultPVID)
			if err != nil {
				return err
			}
		}

		// Configure the untagged membership settings of the port if VLAN ID specified.
		if d.config["vlan"] != "none" {
			_, err = shared.RunCommand("bridge", "vlan", "add", "dev", hostName, "vid", d.config["vlan"], "pvid", "untagged", "master")
			if err != nil {
				return err
			}
		}
	}

	// Add any tagged VLAN memberships.
	if d.config["vlan.tagged"] != "" {
		for _, vlanID := range strings.Split(d.config["vlan.tagged"], ",") {
			vlanID = strings.TrimSpace(vlanID)

			// Reject VLAN ID 0 if specified (as main validation allows VLAN ID 0 to accommodate ovs).
			if vlanID == "0" {
				return fmt.Errorf("VLAN ID 0 is not allowed for native Linux bridges")
			}

			_, err := shared.RunCommand("bridge", "vlan", "add", "dev", hostName, "vid", vlanID)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// setupOVSBridgePortVLANs configures the bridge port with the specified VLAN settings on the openvswitch bridge.
func (d *nicBridged) setupOVSBridgePortVLANs(hostName string) error {
	// Set port on bridge to specified untagged PVID.
	if d.config["vlan"] != "" {
		if d.config["vlan"] == "none" && d.config["vlan.tagged"] == "" {
			return fmt.Errorf("vlan=none is not supported with openvswitch bridges when not using vlan.tagged")
		}

		// Configure the untagged 'native' membership settings of the port if VLAN ID specified.
		// Also set the vlan_mode=access, which will drop any tagged frames.
		// Order is important here, as vlan_mode is set to "access", assuming that vlan.tagged is not used.
		// If vlan.tagged is specified, then we expect it to also change the vlan_mode as needed.
		if d.config["vlan"] != "none" {
			_, err := shared.RunCommand("ovs-vsctl", "set", "port", hostName, "vlan_mode=access", fmt.Sprintf("tag=%s", d.config["vlan"]))
			if err != nil {
				return err
			}
		}
	}

	// Add any tagged VLAN memberships.
	if d.config["vlan.tagged"] != "" {
		vlanIDs := strings.Split(d.config["vlan.tagged"], ",")

		// Remove any spaces from raw config string.
		for i, vlanID := range vlanIDs {
			vlanIDs[i] = strings.TrimSpace(vlanID)
		}

		vlanMode := "trunk" // Default to only allowing tagged frames (drop untagged frames).
		if d.config["vlan"] != "none" {
			// If untagged vlan mode isn't "none" then allow untagged frames for port's 'native' VLAN.
			vlanMode = "native-untagged"
		}

		// Configure the tagged membership settings of the port if VLAN ID specified.
		// Also set the vlan_mode as needed from above.
		// Must come after the ovs-vsctl command used for setting "vlan" mode above so that the correct
		// vlan_mode is retained.
		_, err := shared.RunCommand("ovs-vsctl", "set", "port", hostName, fmt.Sprintf("vlan_mode=%s", vlanMode), fmt.Sprintf("trunks=%s", strings.Join(vlanIDs, ",")))
		if err != nil {
			return err
		}
	}

	return nil
}
