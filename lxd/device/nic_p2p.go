package device

import (
	"fmt"

	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/shared"
)

type nicP2P struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *nicP2P) validateConfig() error {
	if d.instance.Type() != instance.TypeContainer {
		return ErrUnsupportedDevType
	}

	optionalFields := []string{
		"name",
		"mtu",
		"hwaddr",
		"host_name",
		"limits.ingress",
		"limits.egress",
		"limits.max",
		"ipv4.routes",
		"ipv6.routes",
	}
	err := d.config.Validate(nicValidationRules([]string{}, optionalFields))
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicP2P) validateEnvironment() error {
	if d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	return nil
}

// CanHotPlug returns whether the device can be managed whilst the instance is running, it also
// returns a list of fields that can be updated without triggering a device remove & add.
func (d *nicP2P) CanHotPlug() (bool, []string) {
	return true, []string{"limits.ingress", "limits.egress", "limits.max", "ipv4.routes", "ipv6.routes"}
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicP2P) Start() (*RunConfig, error) {
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

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := RunConfig{}
	runConf.NetworkInterface = []RunConfigItem{
		{Key: "name", Value: d.config["name"]},
		{Key: "type", Value: "phys"},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: peerName},
	}

	return &runConf, nil
}

// Update applies configuration changes to a started device.
func (d *nicP2P) Update(oldConfig config.Device, isRunning bool) error {
	if !isRunning {
		return nil
	}

	err := d.validateEnvironment()
	if err != nil {
		return err
	}

	v := d.volatileGet()

	// Apply and host-side limits and routes.
	err = networkSetupHostVethDevice(d.config, oldConfig, v)
	if err != nil {
		return err
	}

	return nil
}

// Stop is run when the device is removed from the instance.
func (d *nicP2P) Stop() (*RunConfig, error) {
	runConf := RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicP2P) postStop() error {
	defer d.volatileSet(map[string]string{
		"host_name": "",
	})

	v := d.volatileGet()

	if d.config["host_name"] == "" {
		d.config["host_name"] = v["host_name"]
	}

	if d.config["host_name"] != "" && shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["host_name"])) {
		// Removing host-side end of veth pair will delete the peer end too.
		err := NetworkRemoveInterface(d.config["host_name"])
		if err != nil {
			return fmt.Errorf("Failed to remove interface %s: %s", d.config["host_name"], err)
		}
	}

	return nil
}
