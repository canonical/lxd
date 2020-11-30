package device

import (
	"fmt"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/resources"
	"github.com/lxc/lxd/shared"
)

type infinibandPhysical struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *infinibandPhysical) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{"parent"}
	optionalFields := []string{
		"name",
		"mtu",
		"hwaddr",
	}

	rules := nicValidationRules(requiredFields, optionalFields)
	rules["hwaddr"] = func(value string) error {
		if value == "" {
			return nil
		}

		return infinibandValidMAC(value)
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *infinibandPhysical) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *infinibandPhysical) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	saveData := make(map[string]string)

	// Load network interface info.
	nics, err := resources.GetNetwork()
	if err != nil {
		return nil, err
	}

	// Filter the network interfaces to just infiniband devices related to parent.
	ibDevs := infinibandDevices(nics, d.config["parent"])
	ibDev, found := ibDevs[d.config["parent"]]
	if !found {
		return nil, fmt.Errorf("Specified infiniband device \"%s\" not found", d.config["parent"])
	}

	saveData["host_name"] = ibDev.ID

	// Record hwaddr and mtu before potentially modifying them.
	err = networkSnapshotPhysicalNic(saveData["host_name"], saveData)
	if err != nil {
		return nil, err
	}

	// Set the MAC address.
	if d.config["hwaddr"] != "" {
		err := infinibandSetDevMAC(saveData["host_name"], d.config["hwaddr"])
		if err != nil {
			return nil, fmt.Errorf("Failed to set the MAC address: %s", err)
		}
	}

	// Set the MTU.
	if d.config["mtu"] != "" {
		err = network.InterfaceSetMTU(saveData["host_name"], d.config["mtu"])
		if err != nil {
			return nil, err
		}
	}

	runConf := deviceConfig.RunConfig{}

	// Configure runConf with infiniband setup instructions.
	err = infinibandAddDevices(d.state, d.inst.DevicesPath(), d.name, ibDev, &runConf)
	if err != nil {
		return nil, err
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "name", Value: d.config["name"]},
		{Key: "type", Value: "phys"},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: saveData["host_name"]},
	}

	if d.inst.Type() == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface,
			[]deviceConfig.RunConfigItem{
				{Key: "devName", Value: d.name},
			}...)
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *infinibandPhysical) Stop() (*deviceConfig.RunConfig, error) {
	v := d.volatileGet()
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
		NetworkInterface: []deviceConfig.RunConfigItem{
			{Key: "link", Value: v["host_name"]},
		},
	}

	err := unixDeviceRemove(d.inst.DevicesPath(), IBDevPrefix, d.name, "", &runConf)
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
	err := unixDeviceDeleteFiles(d.state, d.inst.DevicesPath(), IBDevPrefix, d.name, "")
	if err != nil {
		return fmt.Errorf("Failed to delete files for device '%s': %v", d.name, err)
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
