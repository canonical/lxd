package device

import (
	"fmt"
	"os"
	"strings"

	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/ip"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/validate"
)

const nicRoutedIPv4GW = "169.254.0.1"
const nicRoutedIPv6GW = "fe80::1"

type nicRouted struct {
	deviceCommon
}

// CanHotPlug returns whether the device can be managed whilst the instance is running.
func (d *nicRouted) CanHotPlug() bool {
	return false
}

// UpdatableFields returns a list of fields that can be updated without triggering a device remove & add.
func (d *nicRouted) UpdatableFields(oldDevice Type) []string {
	// Check old and new device types match.
	_, match := oldDevice.(*nicRouted)
	if !match {
		return []string{}
	}

	return []string{"limits.ingress", "limits.egress", "limits.max"}
}

// validateConfig checks the supplied config for correctness.
func (d *nicRouted) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container) {
		return ErrUnsupportedDevType
	}

	err := d.isUniqueWithGatewayAutoMode(instConf)
	if err != nil {
		return err
	}

	requiredFields := []string{}
	optionalFields := []string{
		"name",
		"parent",
		"mtu",
		"hwaddr",
		"host_name",
		"vlan",
		"limits.ingress",
		"limits.egress",
		"limits.max",
		"ipv4.gateway",
		"ipv6.gateway",
		"ipv4.host_address",
		"ipv6.host_address",
		"ipv4.host_table",
		"ipv6.host_table",
		"gvrp",
	}

	rules := nicValidationRules(requiredFields, optionalFields, instConf)
	rules["ipv4.address"] = validate.Optional(validate.IsNetworkAddressV4List)
	rules["ipv6.address"] = validate.Optional(validate.IsNetworkAddressV6List)
	rules["gvrp"] = validate.Optional(validate.IsBool)

	err = d.config.Validate(rules)
	if err != nil {
		return err
	}

	// Detect duplicate IPs in config.
	for _, key := range []string{"ipv4.address", "ipv6.address"} {
		ips := make(map[string]struct{})

		if d.config[key] != "" {
			for _, addr := range strings.Split(d.config[key], ",") {
				addr = strings.TrimSpace(addr)
				if _, dupe := ips[addr]; dupe {
					return fmt.Errorf("Duplicate address %q in %q", addr, key)
				}

				ips[addr] = struct{}{}
			}
		}
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicRouted) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	extensions := d.state.OS.LXCFeatures
	if !extensions["network_veth_router"] || !extensions["network_l2proxy"] {
		return fmt.Errorf("Requires liblxc has following API extensions: network_veth_router, network_l2proxy")
	}

	if d.config["parent"] != "" && !network.InterfaceExists(d.config["parent"]) {
		return fmt.Errorf("Parent device %q doesn't exist", d.config["parent"])
	}

	if d.config["parent"] == "" && d.config["vlan"] != "" {
		return fmt.Errorf("The vlan setting can only be used when combined with a parent interface")
	}

	// Check necessary "all" sysctls are configured for use with l2proxy parent for routed mode.
	if d.config["parent"] != "" && d.config["ipv6.address"] != "" {
		// net.ipv6.conf.all.forwarding=1 is required to enable general packet forwarding for IPv6.
		ipv6FwdPath := fmt.Sprintf("net/ipv6/conf/%s/forwarding", "all")
		sysctlVal, err := util.SysctlGet(ipv6FwdPath)
		if err != nil {
			return fmt.Errorf("Error reading net sysctl %s: %v", ipv6FwdPath, err)
		}
		if sysctlVal != "1\n" {
			return fmt.Errorf("Routed mode requires sysctl net.ipv6.conf.%s.forwarding=1", "all")
		}

		// net.ipv6.conf.all.proxy_ndp=1 is needed otherwise unicast neighbour solicitations are rejected.
		// This causes periodic latency spikes every 15-20s as the neighbour has to resort to using
		// multicast NDP resolution and expires the previous neighbour entry.
		ipv6ProxyNdpPath := fmt.Sprintf("net/ipv6/conf/%s/proxy_ndp", "all")
		sysctlVal, err = util.SysctlGet(ipv6ProxyNdpPath)
		if err != nil {
			return fmt.Errorf("Error reading net sysctl %s: %v", ipv6ProxyNdpPath, err)
		}
		if sysctlVal != "1\n" {
			return fmt.Errorf("Routed mode requires sysctl net.ipv6.conf.%s.proxy_ndp=1", "all")
		}
	}

	// Generate effective parent name, including the VLAN part if option used.
	effectiveParentName := network.GetHostDevice(d.config["parent"], d.config["vlan"])

	// If the effective parent doesn't exist and the vlan option is specified, it means we are going to create
	// the VLAN parent at start, and we will configure the needed sysctls so don't need to check them yet.
	if d.config["vlan"] != "" && network.InterfaceExists(effectiveParentName) {
		return nil
	}

	// Check necessary sysctls are configured for use with l2proxy parent for routed mode.
	if effectiveParentName != "" && d.config["ipv4.address"] != "" {
		ipv4FwdPath := fmt.Sprintf("net/ipv4/conf/%s/forwarding", effectiveParentName)
		sysctlVal, err := util.SysctlGet(ipv4FwdPath)
		if err != nil {
			return fmt.Errorf("Error reading net sysctl %s: %v", ipv4FwdPath, err)
		}
		if sysctlVal != "1\n" {
			// Replace . in parent name with / for sysctl formatting.
			return fmt.Errorf("Routed mode requires sysctl net.ipv4.conf.%s.forwarding=1", strings.Replace(effectiveParentName, ".", "/", -1))
		}
	}

	// Check necessary devic specific sysctls are configured for use with l2proxy parent for routed mode.
	if effectiveParentName != "" && d.config["ipv6.address"] != "" {
		ipv6FwdPath := fmt.Sprintf("net/ipv6/conf/%s/forwarding", effectiveParentName)
		sysctlVal, err := util.SysctlGet(ipv6FwdPath)
		if err != nil {
			return fmt.Errorf("Error reading net sysctl %s: %v", ipv6FwdPath, err)
		}
		if sysctlVal != "1\n" {
			// Replace . in parent name with / for sysctl formatting.
			return fmt.Errorf("Routed mode requires sysctl net.ipv6.conf.%s.forwarding=1", strings.Replace(effectiveParentName, ".", "/", -1))
		}

		ipv6ProxyNdpPath := fmt.Sprintf("net/ipv6/conf/%s/proxy_ndp", effectiveParentName)
		sysctlVal, err = util.SysctlGet(ipv6ProxyNdpPath)
		if err != nil {
			return fmt.Errorf("Error reading net sysctl %s: %v", ipv6ProxyNdpPath, err)
		}
		if sysctlVal != "1\n" {
			// Replace . in parent name with / for sysctl formatting.
			return fmt.Errorf("Routed mode requires sysctl net.ipv6.conf.%s.proxy_ndp=1", strings.Replace(effectiveParentName, ".", "/", -1))
		}
	}

	return nil
}

// Start is run when the instance is starting up (Routed mode doesn't support hot plugging).
func (d *nicRouted) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	// Lock to avoid issues with containers starting in parallel.
	networkCreateSharedDeviceLock.Lock()
	defer networkCreateSharedDeviceLock.Unlock()

	saveData := make(map[string]string)

	// Decide which parent we should use based on VLAN setting.
	parentName := ""
	if d.config["parent"] != "" {
		parentName = network.GetHostDevice(d.config["parent"], d.config["vlan"])

		statusDev, err := networkCreateVlanDeviceIfNeeded(d.state, d.config["parent"], parentName, d.config["vlan"], shared.IsTrue(d.config["gvrp"]))
		if err != nil {
			return nil, err
		}

		// Record whether we created this device or not so it can be removed on stop.
		saveData["last_state.created"] = fmt.Sprintf("%t", statusDev != "existing")

		// If we created a VLAN interface, we need to setup the sysctls on that interface.
		if statusDev == "created" {
			err := d.setupParentSysctls(parentName)
			if err != nil {
				return nil, err
			}
		}
	}

	hostName := d.config["host_name"]
	if hostName == "" {
		hostName = network.RandomDevName("veth")
	}
	saveData["host_name"] = hostName

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	nic := []deviceConfig.RunConfigItem{
		{Key: "type", Value: "veth"},
		{Key: "name", Value: d.config["name"]},
		{Key: "flags", Value: "up"},
		{Key: "veth.mode", Value: "router"},
		{Key: "veth.pair", Value: saveData["host_name"]},
	}

	// If there is a designated parent interface, activate the layer2 proxy mode to advertise
	// the instance's IPs over that interface using proxy APR/NDP.
	if parentName != "" {
		nic = append(nic,
			deviceConfig.RunConfigItem{Key: "l2proxy", Value: "1"},
			deviceConfig.RunConfigItem{Key: "link", Value: parentName},
		)
	}

	if d.config["mtu"] != "" {
		nic = append(nic, deviceConfig.RunConfigItem{Key: "mtu", Value: d.config["mtu"]})
	}

	if d.config["ipv4.address"] != "" {
		for _, addr := range strings.Split(d.config["ipv4.address"], ",") {
			addr = strings.TrimSpace(addr)
			nic = append(nic, deviceConfig.RunConfigItem{Key: "ipv4.address", Value: fmt.Sprintf("%s/32", addr)})
		}

		if nicHasAutoGateway(d.config["ipv4.gateway"]) {
			// Use a fixed link-local address as the next-hop default gateway.
			nic = append(nic, deviceConfig.RunConfigItem{Key: "ipv4.gateway", Value: d.ipv4HostAddress()})
		}
	}

	if d.config["ipv6.address"] != "" {
		for _, addr := range strings.Split(d.config["ipv6.address"], ",") {
			addr = strings.TrimSpace(addr)
			nic = append(nic, deviceConfig.RunConfigItem{Key: "ipv6.address", Value: fmt.Sprintf("%s/128", addr)})
		}

		if nicHasAutoGateway(d.config["ipv6.gateway"]) {
			// Use a fixed link-local address as the next-hop default gateway.
			nic = append(nic, deviceConfig.RunConfigItem{Key: "ipv6.gateway", Value: d.ipv6HostAddress()})
		}
	}

	runConf.NetworkInterface = nic
	runConf.PostHooks = append(runConf.PostHooks, d.postStart)
	return &runConf, nil
}

// setupParentSysctls configures the required sysctls on the parent to allow l2proxy to work.
// Because of our policy not to modify sysctls on existing interfaces, this should only be called
// if we created the parent interface.
func (d *nicRouted) setupParentSysctls(parentName string) error {
	if d.config["ipv4.address"] != "" {
		// Set necessary sysctls for use with l2proxy parent in routed mode.
		ipv4FwdPath := fmt.Sprintf("net/ipv4/conf/%s/forwarding", parentName)
		err := util.SysctlSet(ipv4FwdPath, "1")
		if err != nil {
			return fmt.Errorf("Error setting net sysctl %s: %v", ipv4FwdPath, err)
		}
	}

	if d.config["ipv6.address"] != "" {
		// Set necessary sysctls use with l2proxy parent in routed mode.
		ipv6FwdPath := fmt.Sprintf("net/ipv6/conf/%s/forwarding", parentName)
		err := util.SysctlSet(ipv6FwdPath, "1")
		if err != nil {
			return fmt.Errorf("Error setting net sysctl %s: %v", ipv6FwdPath, err)
		}

		ipv6ProxyNdpPath := fmt.Sprintf("net/ipv6/conf/%s/proxy_ndp", parentName)
		err = util.SysctlSet(ipv6ProxyNdpPath, "1")
		if err != nil {
			return fmt.Errorf("Error setting net sysctl %s: %v", ipv6ProxyNdpPath, err)
		}
	}

	return nil
}

// Update returns an error as most devices do not support live updates without being restarted.
func (d *nicRouted) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	v := d.volatileGet()

	// If instance is running, apply host side limits.
	if isRunning {
		err := d.validateEnvironment()
		if err != nil {
			return err
		}

		// Populate device config with volatile fields if needed.
		networkVethFillFromVolatile(d.config, v)

		// Apply host-side limits.
		err = networkSetupHostVethLimits(d.config)
		if err != nil {
			return err
		}
	}

	return nil
}

// postStart is run after the instance is started.
func (d *nicRouted) postStart() error {
	v := d.volatileGet()

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, v)

	// Apply host-side limits.
	err := networkSetupHostVethLimits(d.config)
	if err != nil {
		return err
	}

	// Attempt to disable IPv6 router advertisement acceptance.
	err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/accept_ra", d.config["host_name"]), "0")
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Prevent source address spoofing by requiring a return path.
	err = util.SysctlSet(fmt.Sprintf("net/ipv4/conf/%s/rp_filter", d.config["host_name"]), "1")
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Apply firewall rules for reverse path filtering of IPv4 and IPv6.
	err = d.state.Firewall.InstanceSetupRPFilter(d.inst.Project(), d.inst.Name(), d.name, d.config["host_name"])
	if err != nil {
		return errors.Wrapf(err, "Error setting up reverse path filter")
	}

	if d.config["ipv4.address"] != "" {
		// Add link-local gateway IPs to the host end of the veth pair. This ensures that
		// liveness detection of the gateways inside the instance work and ensure that traffic
		// doesn't periodically halt whilst ARP is re-detected.
		addr := &ip.Addr{
			DevName: d.config["host_name"],
			Address: fmt.Sprintf("%s/32", d.ipv4HostAddress()),
			Family:  ip.FamilyV4,
		}
		err := addr.Add()
		if err != nil {
			return err
		}

		// Add static routes to instance IPs to custom routing tables if specified.
		// This is in addition to the static route added by liblxc to the main routing table, which
		// is still critical to ensure that reverse path filtering doesn't kick in blocking traffic
		// from the instance.
		if d.config["ipv4.host_table"] != "" {
			for _, addr := range strings.Split(d.config["ipv4.address"], ",") {
				addr = strings.TrimSpace(addr)
				r := &ip.Route{
					DevName: d.config["host_name"],
					Route:   fmt.Sprintf("%s/32", addr),
					Table:   d.config["ipv4.host_table"],
					Family:  ip.FamilyV4,
				}
				err := r.Add()
				if err != nil {
					return err
				}
			}
		}
	}

	if d.config["ipv6.address"] != "" {
		// Add link-local gateway IPs to the host end of the veth pair. This ensures that
		// liveness detection of the gateways inside the instance work and ensure that traffic
		// doesn't periodically halt whilst NDP is re-detected.
		addr := &ip.Addr{
			DevName: d.config["host_name"],
			Address: fmt.Sprintf("%s/128", d.ipv6HostAddress()),
			Family:  ip.FamilyV6,
		}
		err := addr.Add()
		if err != nil {
			return err
		}

		// Add static routes to instance IPs to custom routing tables if specified.
		// This is in addition to the static route added by liblxc to the main routing table, which
		// is still critical to ensure that reverse path filtering doesn't kick in blocking traffic
		// from the instance.
		if d.config["ipv6.host_table"] != "" {
			for _, addr := range strings.Split(d.config["ipv6.address"], ",") {
				addr = strings.TrimSpace(addr)
				r := &ip.Route{
					DevName: d.config["host_name"],
					Route:   fmt.Sprintf("%s/128", addr),
					Table:   d.config["ipv6.host_table"],
					Family:  ip.FamilyV6,
				}
				err := r.Add()
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// Stop is run when the device is removed from the instance.
func (d *nicRouted) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicRouted) postStop() error {
	defer d.volatileSet(map[string]string{
		"last_state.created": "",
		"host_name":          "",
	})

	v := d.volatileGet()

	networkVethFillFromVolatile(d.config, v)

	errs := []error{}

	// Delete host-side end of veth pair if not removed by liblxc.
	if network.InterfaceExists(d.config["host_name"]) {
		// Removing host-side end of veth pair will delete the peer end too.
		err := network.InterfaceRemove(d.config["host_name"])
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "Failed to remove interface %q", d.config["host_name"]))
		}
	}

	// Delete IP neighbour proxy entries on the parent if they haven't been removed by liblxc.
	for _, key := range []string{"ipv4.address", "ipv6.address"} {
		if d.config[key] != "" {
			for _, addr := range strings.Split(d.config[key], ",") {
				neigh := &ip.Neigh{
					DevName: d.config["parent"],
					Proxy:   strings.TrimSpace(addr),
				}
				neigh.Delete()
			}
		}
	}

	// This will delete the parent interface if we created it for VLAN parent.
	if shared.IsTrue(v["last_state.created"]) {
		parentName := network.GetHostDevice(d.config["parent"], d.config["vlan"])
		err := networkRemoveInterfaceIfNeeded(d.state, parentName, d.inst, d.config["parent"], d.config["vlan"])
		if err != nil {
			errs = append(errs, err)
		}
	}

	// Remove reverse path filters.
	err := d.state.Firewall.InstanceClearRPFilter(d.inst.Project(), d.inst.Name(), d.name)
	if err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("%v", errs)
	}

	return nil
}

func (d *nicRouted) ipv4HostAddress() string {
	if d.config["ipv4.host_address"] != "" {
		return d.config["ipv4.host_address"]
	}

	return nicRoutedIPv4GW
}

func (d *nicRouted) ipv6HostAddress() string {
	if d.config["ipv6.host_address"] != "" {
		return d.config["ipv6.host_address"]
	}

	return nicRoutedIPv6GW
}

func (d *nicRouted) isUniqueWithGatewayAutoMode(instConf instance.ConfigReader) error {
	instDevs := instConf.ExpandedDevices()
	for _, k := range []string{"ipv4.gateway", "ipv6.gateway"} {
		if d.config[k] != "auto" && d.config[k] != "" {
			continue // nothing to do as auto not being used.
		}

		// Check other routed NIC devices don't have auto set.
		for nicName, nicConfig := range instDevs {
			if nicName == d.name || nicConfig["nictype"] != "routed" {
				continue // Skip ourselves.
			}

			if nicConfig[k] == "auto" || nicConfig[k] == "" {
				return fmt.Errorf("Existing NIC %q already uses %q in auto mode", nicName, k)
			}
		}
	}

	return nil
}
