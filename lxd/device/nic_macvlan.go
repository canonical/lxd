package device

import (
	"fmt"

	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/shared"
)

type nicMACVLAN struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *nicMACVLAN) validateConfig() error {
	if d.instance.Type() != instancetype.Container {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{"parent"}
	optionalFields := []string{"name", "mtu", "hwaddr", "vlan", "maas.subnet.ipv4", "maas.subnet.ipv6"}
	err := d.config.Validate(nicValidationRules(requiredFields, optionalFields))
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicMACVLAN) validateEnvironment() error {
	if d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicMACVLAN) Start() (*RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	// Lock to avoid issues with containers starting in parallel.
	networkCreateSharedDeviceLock.Lock()
	defer networkCreateSharedDeviceLock.Unlock()

	saveData := make(map[string]string)

	// Decide which parent we should use based on VLAN setting.
	parentName := NetworkGetHostDevice(d.config["parent"], d.config["vlan"])

	// Record the temporary device name used for deletion later.
	saveData["host_name"] = NetworkRandomDevName("mac")

	// Create VLAN parent device if needed.
	statusDev, err := NetworkCreateVlanDeviceIfNeeded(d.state, d.config["parent"], parentName, d.config["vlan"])
	if err != nil {
		return nil, err
	}

	// Record whether we created the parent device or not so it can be removed on stop.
	saveData["last_state.created"] = fmt.Sprintf("%t", statusDev != "existing")

	// Create MACVLAN interface.
	_, err = shared.RunCommand("ip", "link", "add", "dev", saveData["host_name"], "link", parentName, "type", "macvlan", "mode", "bridge")
	if err != nil {
		return nil, err
	}

	// Set the MAC address.
	if d.config["hwaddr"] != "" {
		_, err := shared.RunCommand("ip", "link", "set", "dev", saveData["host_name"], "address", d.config["hwaddr"])
		if err != nil {
			if statusDev == "created" {
				NetworkRemoveInterface(saveData["host_name"])
			}

			return nil, fmt.Errorf("Failed to set the MAC address: %s", err)
		}
	}

	// Set the MTU.
	if d.config["mtu"] != "" {
		_, err := shared.RunCommand("ip", "link", "set", "dev", saveData["host_name"], "mtu", d.config["mtu"])
		if err != nil {
			if statusDev == "created" {
				NetworkRemoveInterface(saveData["host_name"])
			}

			return nil, fmt.Errorf("Failed to set the MTU: %s", err)
		}
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := RunConfig{}
	runConf.NetworkInterface = []RunConfigItem{
		{Key: "name", Value: d.config["name"]},
		{Key: "type", Value: "phys"},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: saveData["host_name"]},
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *nicMACVLAN) Stop() (*RunConfig, error) {
	v := d.volatileGet()
	runConf := RunConfig{
		PostHooks: []func() error{d.postStop},
		NetworkInterface: []RunConfigItem{
			{Key: "link", Value: v["host_name"]},
		},
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicMACVLAN) postStop() error {
	defer d.volatileSet(map[string]string{
		"host_name":          "",
		"last_state.hwaddr":  "",
		"last_state.mtu":     "",
		"last_state.created": "",
	})

	errs := []error{}
	v := d.volatileGet()

	// Delete the detached device.
	if v["host_name"] != "" && shared.PathExists(fmt.Sprintf("/sys/class/net/%s", v["host_name"])) {
		err := NetworkRemoveInterface(v["host_name"])
		if err != nil {
			errs = append(errs, err)
		}
	}

	// This will delete the parent interface if we created it for VLAN parent.
	if shared.IsTrue(v["last_state.created"]) {
		parentName := NetworkGetHostDevice(d.config["parent"], d.config["vlan"])
		err := NetworkRemoveInterfaceIfNeeded(d.state, parentName, d.instance, d.config["parent"], d.config["vlan"])
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%v", errs)
	}

	return nil
}
