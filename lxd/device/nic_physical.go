package device

import (
	"fmt"

	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/shared"
)

type nicPhysical struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *nicPhysical) validateConfig() error {
	if d.instance.Type() != instancetype.Container {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{"parent"}
	optionalFields := []string{
		"name",
		"mtu",
		"hwaddr",
		"vlan",
		"maas.subnet.ipv4",
		"maas.subnet.ipv6",
	}
	err := d.config.Validate(nicValidationRules(requiredFields, optionalFields))
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicPhysical) validateEnvironment() error {
	if d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicPhysical) Start() (*RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	// Lock to avoid issues with containers starting in parallel.
	networkCreateSharedDeviceLock.Lock()
	defer networkCreateSharedDeviceLock.Unlock()

	saveData := make(map[string]string)

	// Record the host_name device used for restoration later.
	saveData["host_name"] = NetworkGetHostDevice(d.config["parent"], d.config["vlan"])
	statusDev, err := NetworkCreateVlanDeviceIfNeeded(d.state, d.config["parent"], saveData["host_name"], d.config["vlan"])
	if err != nil {
		return nil, err
	}

	// Record whether we created this device or not so it can be removed on stop.
	saveData["last_state.created"] = fmt.Sprintf("%t", statusDev != "existing")

	// If we return from this function with an error, ensure we clean up created device.
	defer func() {
		if err != nil && statusDev == "created" {
			NetworkRemoveInterface(saveData["host_name"])
		}
	}()

	// If we didn't create the device we should track various properties so we can
	// restore them when the instance is stopped or the device is detached.
	if statusDev == "existing" {
		err = networkSnapshotPhysicalNic(saveData["host_name"], saveData)
		if err != nil {
			return nil, err
		}
	}

	// Set the MAC address.
	if d.config["hwaddr"] != "" {
		_, err := shared.RunCommand("ip", "link", "set", "dev", saveData["host_name"], "address", d.config["hwaddr"])
		if err != nil {
			return nil, fmt.Errorf("Failed to set the MAC address: %s", err)
		}
	}

	// Set the MTU.
	if d.config["mtu"] != "" {
		_, err := shared.RunCommand("ip", "link", "set", "dev", saveData["host_name"], "mtu", d.config["mtu"])
		if err != nil {
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
func (d *nicPhysical) Stop() (*RunConfig, error) {
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
func (d *nicPhysical) postStop() error {
	defer d.volatileSet(map[string]string{
		"host_name":          "",
		"last_state.hwaddr":  "",
		"last_state.mtu":     "",
		"last_state.created": "",
	})

	v := d.volatileGet()
	hostName := NetworkGetHostDevice(d.config["parent"], d.config["vlan"])
	err := networkRestorePhysicalNic(hostName, v)
	if err != nil {
		return err
	}

	return nil
}
