package device

import (
	"fmt"
	"strings"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/validate"
)

type nicIPVLAN struct {
	deviceCommon
}

// CanHotPlug returns whether the device can be managed whilst the instance is running,
func (d *nicIPVLAN) CanHotPlug() bool {
	return false
}

// validateConfig checks the supplied config for correctness.
func (d *nicIPVLAN) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{"parent"}
	optionalFields := []string{
		"name",
		"mtu",
		"hwaddr",
		"vlan",
		"ipv4.gateway",
		"ipv6.gateway",
	}

	rules := nicValidationRules(requiredFields, optionalFields)
	rules["ipv4.address"] = func(value string) error {
		if value == "" {
			return nil
		}

		return validate.IsNetworkAddressV4List(value)
	}
	rules["ipv6.address"] = func(value string) error {
		if value == "" {
			return nil
		}

		return validate.IsNetworkAddressV6List(value)
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicIPVLAN) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	extensions := d.state.OS.LXCFeatures
	if !extensions["network_ipvlan"] || !extensions["network_l2proxy"] || !extensions["network_gateway_device_route"] {
		return fmt.Errorf("Requires liblxc has following API extensions: network_ipvlan, network_l2proxy, network_gateway_device_route")
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	if d.config["parent"] == "" && d.config["vlan"] != "" {
		return fmt.Errorf("The vlan setting can only be used when combined with a parent interface")
	}

	// Generate effective parent name, including the VLAN part if option used.
	effectiveParentName := network.GetHostDevice(d.config["parent"], d.config["vlan"])

	// If the effective parent doesn't exist and the vlan option is specified, it means we are going to create
	// the VLAN parent at start, and we will configure the needed sysctls so don't need to check them yet.
	if d.config["vlan"] != "" && !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", effectiveParentName)) {
		return nil
	}

	if d.config["ipv4.address"] != "" {
		// Check necessary sysctls are configured for use with l2proxy parent in IPVLAN l3s mode.
		ipv4FwdPath := fmt.Sprintf("net/ipv4/conf/%s/forwarding", effectiveParentName)
		sysctlVal, err := util.SysctlGet(ipv4FwdPath)
		if err != nil || sysctlVal != "1\n" {
			return fmt.Errorf("Error reading net sysctl %s: %v", ipv4FwdPath, err)
		}
		if sysctlVal != "1\n" {
			// Replace . in parent name with / for sysctl formatting.
			return fmt.Errorf("IPVLAN in L3S mode requires sysctl net.ipv4.conf.%s.forwarding=1", strings.Replace(effectiveParentName, ".", "/", -1))
		}
	}

	if d.config["ipv6.address"] != "" {
		// Check necessary sysctls are configured for use with l2proxy parent in IPVLAN l3s mode.
		ipv6FwdPath := fmt.Sprintf("net/ipv6/conf/%s/forwarding", effectiveParentName)
		sysctlVal, err := util.SysctlGet(ipv6FwdPath)
		if err != nil {
			return fmt.Errorf("Error reading net sysctl %s: %v", ipv6FwdPath, err)
		}
		if sysctlVal != "1\n" {
			// Replace . in parent name with / for sysctl formatting.
			return fmt.Errorf("IPVLAN in L3S mode requires sysctl net.ipv6.conf.%s.forwarding=1", strings.Replace(effectiveParentName, ".", "/", -1))
		}

		ipv6ProxyNdpPath := fmt.Sprintf("net/ipv6/conf/%s/proxy_ndp", effectiveParentName)
		sysctlVal, err = util.SysctlGet(ipv6ProxyNdpPath)
		if err != nil {
			return fmt.Errorf("Error reading net sysctl %s: %v", ipv6ProxyNdpPath, err)
		}
		if sysctlVal != "1\n" {
			// Replace . in parent name with / for sysctl formatting.
			return fmt.Errorf("IPVLAN in L3S mode requires sysctl net.ipv6.conf.%s.proxy_ndp=1", strings.Replace(effectiveParentName, ".", "/", -1))
		}
	}

	return nil
}

// Start is run when the instance is starting up (IPVLAN doesn't support hot plugging).
func (d *nicIPVLAN) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	// Lock to avoid issues with containers starting in parallel.
	networkCreateSharedDeviceLock.Lock()
	defer networkCreateSharedDeviceLock.Unlock()

	saveData := make(map[string]string)

	// Decide which parent we should use based on VLAN setting.
	parentName := network.GetHostDevice(d.config["parent"], d.config["vlan"])

	statusDev, err := networkCreateVlanDeviceIfNeeded(d.state, d.config["parent"], parentName, d.config["vlan"])
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

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	nic := []deviceConfig.RunConfigItem{
		{Key: "name", Value: d.config["name"]},
		{Key: "type", Value: "ipvlan"},
		{Key: "flags", Value: "up"},
		{Key: "ipvlan.mode", Value: "l3s"},
		{Key: "ipvlan.isolation", Value: "bridge"},
		{Key: "l2proxy", Value: "1"},
		{Key: "link", Value: parentName},
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
			nic = append(nic, deviceConfig.RunConfigItem{Key: "ipv4.gateway", Value: "dev"})
		}
	}

	if d.config["ipv6.address"] != "" {
		for _, addr := range strings.Split(d.config["ipv6.address"], ",") {
			addr = strings.TrimSpace(addr)
			nic = append(nic, deviceConfig.RunConfigItem{Key: "ipv6.address", Value: fmt.Sprintf("%s/128", addr)})
		}

		if nicHasAutoGateway(d.config["ipv6.gateway"]) {
			nic = append(nic, deviceConfig.RunConfigItem{Key: "ipv6.gateway", Value: "dev"})
		}
	}

	runConf.NetworkInterface = nic
	return &runConf, nil
}

// setupParentSysctls configures the required sysctls on the parent to allow l2proxy to work.
// Because of our policy not to modify sysctls on existing interfaces, this should only be called
// if we created the parent interface.
func (d *nicIPVLAN) setupParentSysctls(parentName string) error {
	if d.config["ipv4.address"] != "" {
		// Set necessary sysctls for use with l2proxy parent in IPVLAN l3s mode.
		ipv4FwdPath := fmt.Sprintf("net/ipv4/conf/%s/forwarding", parentName)
		err := util.SysctlSet(ipv4FwdPath, "1")
		if err != nil {
			return fmt.Errorf("Error setting net sysctl %s: %v", ipv4FwdPath, err)
		}
	}

	if d.config["ipv6.address"] != "" {
		// Set necessary sysctls use with l2proxy parent in IPVLAN l3s mode.
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

// Stop is run when the device is removed from the instance.
func (d *nicIPVLAN) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicIPVLAN) postStop() error {
	defer d.volatileSet(map[string]string{
		"last_state.created": "",
	})

	v := d.volatileGet()

	// This will delete the parent interface if we created it for VLAN parent.
	if shared.IsTrue(v["last_state.created"]) {
		parentName := network.GetHostDevice(d.config["parent"], d.config["vlan"])
		err := networkRemoveInterfaceIfNeeded(d.state, parentName, d.inst, d.config["parent"], d.config["vlan"])
		if err != nil {
			return err
		}
	}

	return nil
}
