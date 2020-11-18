package device

import (
	"fmt"

	deviceConfig "github.com/grant-he/lxd/lxd/device/config"
	"github.com/grant-he/lxd/lxd/instance"
	"github.com/grant-he/lxd/lxd/instance/instancetype"
	"github.com/grant-he/lxd/lxd/network"
	"github.com/grant-he/lxd/lxd/revert"
	"github.com/grant-he/lxd/shared"
)

type nicP2P struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *nicP2P) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
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
		"boot.priority",
	}
	err := d.config.Validate(nicValidationRules([]string{}, optionalFields))
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicP2P) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
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
func (d *nicP2P) Start() (*deviceConfig.RunConfig, error) {
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
			saveData["host_name"] = network.RandomDevName("veth")
		}
		peerName, err = networkCreateVethPair(saveData["host_name"], d.config)
	} else if d.inst.Type() == instancetype.VM {
		if saveData["host_name"] == "" {
			saveData["host_name"] = network.RandomDevName("tap")
		}
		peerName = saveData["host_name"] // VMs use the host_name to link to the TAP FD.
		err = networkCreateTap(saveData["host_name"], d.config)
	}

	if err != nil {
		return nil, err
	}

	revert.Add(func() { network.InterfaceRemove(saveData["host_name"]) })

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, saveData)

	// Apply host-side routes.
	err = networkSetupHostVethRoutes(d.state, d.config, nil, saveData)
	if err != nil {
		return nil, err
	}

	// Apply host-side limits.
	err = networkSetupHostVethLimits(d.config)
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
func (d *nicP2P) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	oldConfig := oldDevices[d.name]

	if !isRunning {
		return nil
	}

	err := d.validateEnvironment()
	if err != nil {
		return err
	}

	v := d.volatileGet()

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, v)

	// Apply host-side routes.
	err = networkSetupHostVethRoutes(d.state, d.config, oldConfig, v)
	if err != nil {
		return err
	}

	// Apply host-side limits.
	err = networkSetupHostVethLimits(d.config)
	if err != nil {
		return err
	}

	return nil
}

// Stop is run when the device is removed from the instance.
func (d *nicP2P) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
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

	networkVethFillFromVolatile(d.config, v)

	if d.config["host_name"] != "" && shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["host_name"])) {
		// Removing host-side end of veth pair will delete the peer end too.
		err := network.InterfaceRemove(d.config["host_name"])
		if err != nil {
			return fmt.Errorf("Failed to remove interface %s: %s", d.config["host_name"], err)
		}
	}

	return nil
}
