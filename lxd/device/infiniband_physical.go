package device

import (
	"fmt"

	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/shared"
)

type infinibandPhysical struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *infinibandPhysical) validateConfig() error {
	if d.instance.Type() != instance.TypeContainer {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{"parent"}
	optionalFields := []string{
		"name",
		"mtu",
		"hwaddr",
	}
	err := config.ValidateDevice(nicValidationRules(requiredFields, optionalFields), d.config)
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *infinibandPhysical) validateEnvironment() error {
	if d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *infinibandPhysical) Start() (*RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	saveData := make(map[string]string)

	devices, err := infinibandLoadDevices()
	if err != nil {
		return nil, err
	}

	saveData["host_name"] = d.config["parent"]
	ifDev, ok := devices[saveData["host_name"]]
	if !ok {
		return nil, fmt.Errorf("Specified infiniband device \"%s\" not found", saveData["host_name"])
	}

	// Record hwaddr and mtu before potentially modifying them.
	err = networkSnapshotPhysicalNic(saveData["host_name"], saveData)
	if err != nil {
		return nil, err
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

	runConf := RunConfig{}

	// Configure runConf with infiniband setup instructions.
	err = infinibandAddDevices(d.state, d.instance.DevicesPath(), d.name, &ifDev, &runConf)
	if err != nil {
		return nil, err
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf.NetworkInterface = []RunConfigItem{
		{Key: "name", Value: d.config["name"]},
		{Key: "type", Value: "phys"},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: saveData["host_name"]},
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *infinibandPhysical) Stop() (*RunConfig, error) {
	v := d.volatileGet()
	runConf := RunConfig{
		PostHooks: []func() error{d.postStop},
		NetworkInterface: []RunConfigItem{
			{Key: "link", Value: v["host_name"]},
		},
	}

	err := infinibandRemoveDevices(d.instance.DevicesPath(), d.name, &runConf)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *infinibandPhysical) postStop() error {
	defer d.volatileSet(map[string]string{
		"host_name":         "",
		"last_state.hwaddr": "",
		"last_state.mtu":    "",
	})

	// Remove infiniband host files for this device.
	err := infinibandDeleteHostFiles(d.state, d.instance.DevicesPath(), d.name)
	if err != nil {
		return err
	}

	// Restpre hwaddr and mtu.
	v := d.volatileGet()
	if v["host_name"] != "" {
		err := networkRestorePhysicalNic(v["host_name"], v)
		if err != nil {
			return err
		}
	}

	return nil
}
