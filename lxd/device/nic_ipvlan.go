package device

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/shared"
)

type nicIPVLAN struct {
	deviceCommon
}

func (d *nicIPVLAN) CanHotPlug() (bool, []string) {
	return false, []string{}
}

// validateConfig checks the supplied config for correctness.
func (d *nicIPVLAN) validateConfig() error {
	if d.instance.Type() != instancetype.Container {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{"parent"}
	optionalFields := []string{
		"name",
		"mtu",
		"hwaddr",
		"host_name",
		"vlan",
	}

	rules := nicValidationRules(requiredFields, optionalFields)
	rules["ipv4.address"] = func(value string) error {
		if value == "" {
			return nil
		}

		return NetworkValidAddressV4List(value)
	}
	rules["ipv6.address"] = func(value string) error {
		if value == "" {
			return nil
		}

		return NetworkValidAddressV6List(value)
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicIPVLAN) validateEnvironment() error {
	if d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	extensions := d.state.OS.LXCFeatures
	if !extensions["network_ipvlan"] || !extensions["network_l2proxy"] || !extensions["network_gateway_device_route"] {
		return fmt.Errorf("Requires liblxc has following API extensions: network_ipvlan, network_l2proxy, network_gateway_device_route")
	}

	if d.config["ipv4.address"] != "" {
		// Check necessary sysctls are configured for use with l2proxy parent in IPVLAN l3s mode.
		ipv4FwdPath := fmt.Sprintf("ipv4/conf/%s/forwarding", d.config["parent"])
		sysctlVal, err := NetworkSysctlGet(ipv4FwdPath)
		if err != nil || sysctlVal != "1\n" {
			return fmt.Errorf("Error reading net sysctl %s: %v", ipv4FwdPath, err)
		}
		if sysctlVal != "1\n" {
			return fmt.Errorf("IPVLAN in L3S mode requires sysctl net.ipv4.conf.%s.forwarding=1", d.config["parent"])
		}
	}

	if d.config["ipv6.address"] != "" {
		// Check necessary sysctls are configured for use with l2proxy parent in IPVLAN l3s mode.
		ipv6FwdPath := fmt.Sprintf("ipv6/conf/%s/forwarding", d.config["parent"])
		sysctlVal, err := NetworkSysctlGet(ipv6FwdPath)
		if err != nil {
			return fmt.Errorf("Error reading net sysctl %s: %v", ipv6FwdPath, err)
		}
		if sysctlVal != "1\n" {
			return fmt.Errorf("IPVLAN in L3S mode requires sysctl net.ipv6.conf.%s.forwarding=1", d.config["parent"])
		}

		ipv6ProxyNdpPath := fmt.Sprintf("ipv6/conf/%s/proxy_ndp", d.config["parent"])
		sysctlVal, err = NetworkSysctlGet(ipv6ProxyNdpPath)
		if err != nil {
			return fmt.Errorf("Error reading net sysctl %s: %v", ipv6ProxyNdpPath, err)
		}
		if sysctlVal != "1\n" {
			return fmt.Errorf("IPVLAN in L3S mode requires sysctl net.ipv6.conf.%s.proxy_ndp=1", d.config["parent"])
		}
	}

	return nil
}

// Start is run when the instance is starting up (IPVLAN doesn't support hot plugging).
func (d *nicIPVLAN) Start() (*RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	saveData := make(map[string]string)

	// Decide which parent we should use based on VLAN setting.
	parentName := NetworkGetHostDevice(d.config["parent"], d.config["vlan"])

	createdDev, err := NetworkCreateVlanDeviceIfNeeded(d.config["parent"], parentName, d.config["vlan"])
	if err != nil {
		return nil, err
	}

	// Record whether we created this device or not so it can be removed on stop.
	saveData["last_state.created"] = fmt.Sprintf("%t", createdDev)

	// If we created a VLAN interface, we need to setup the sysctls on that interface.
	if createdDev {
		err := d.setupParentSysctls(parentName)
		if err != nil {
			return nil, err
		}
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := RunConfig{}
	nic := []RunConfigItem{
		{Key: "name", Value: d.config["name"]},
		{Key: "type", Value: "ipvlan"},
		{Key: "flags", Value: "up"},
		{Key: "ipvlan.mode", Value: "l3s"},
		{Key: "ipvlan.isolation", Value: "bridge"},
		{Key: "l2proxy", Value: "1"},
		{Key: "link", Value: parentName},
	}

	if d.config["mtu"] != "" {
		nic = append(nic, RunConfigItem{Key: "mtu", Value: d.config["mtu"]})
	}

	if d.config["ipv4.address"] != "" {
		for _, addr := range strings.Split(d.config["ipv4.address"], ",") {
			addr = strings.TrimSpace(addr)
			nic = append(nic, RunConfigItem{Key: "ipv4.address", Value: fmt.Sprintf("%s/32", addr)})
		}

		nic = append(nic, RunConfigItem{Key: "ipv4.gateway", Value: "dev"})
	}

	if d.config["ipv6.address"] != "" {
		for _, addr := range strings.Split(d.config["ipv6.address"], ",") {
			addr = strings.TrimSpace(addr)
			nic = append(nic, RunConfigItem{Key: "ipv6.address", Value: fmt.Sprintf("%s/128", addr)})
		}

		nic = append(nic, RunConfigItem{Key: "ipv6.gateway", Value: "dev"})
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
		ipv4FwdPath := fmt.Sprintf("ipv4/conf/%s/forwarding", parentName)
		err := NetworkSysctlSet(ipv4FwdPath, "1")
		if err != nil {
			return fmt.Errorf("Error setting net sysctl %s: %v", ipv4FwdPath, err)
		}
	}

	if d.config["ipv6.address"] != "" {
		// Set necessary sysctls use with l2proxy parent in IPVLAN l3s mode.
		ipv6FwdPath := fmt.Sprintf("ipv6/conf/%s/forwarding", parentName)
		err := NetworkSysctlSet(ipv6FwdPath, "1")
		if err != nil {
			return fmt.Errorf("Error reading net sysctl %s: %v", ipv6FwdPath, err)
		}

		ipv6ProxyNdpPath := fmt.Sprintf("ipv6/conf/%s/proxy_ndp", parentName)
		err = NetworkSysctlSet(ipv6ProxyNdpPath, "1")
		if err != nil {
			return fmt.Errorf("Error reading net sysctl %s: %v", ipv6ProxyNdpPath, err)
		}
	}

	return nil
}

// Stop is run when the device is removed from the instance.
func (d *nicIPVLAN) Stop() (*RunConfig, error) {
	runConf := RunConfig{
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
		parentName := NetworkGetHostDevice(d.config["parent"], d.config["vlan"])
		err := NetworkRemoveInterface(parentName)
		if err != nil {
			return err
		}
	}

	return nil
}
