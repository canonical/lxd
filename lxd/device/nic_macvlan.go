package device

import (
	"fmt"

	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/ip"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

type nicMACVLAN struct {
	deviceCommon
}

// CanHotPlug returns whether the device can be managed whilst the instance is running. Returns true.
func (d *nicMACVLAN) CanHotPlug() bool {
	return true
}

// validateConfig checks the supplied config for correctness.
func (d *nicMACVLAN) validateConfig(instConf instance.ConfigReader) error {
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
		"vlan",
		"maas.subnet.ipv4",
		"maas.subnet.ipv6",
		"boot.priority",
		"gvrp",
	}

	// Check that if network proeperty is set that conflicting keys are not present.
	if d.config["network"] != "" {
		requiredFields = append(requiredFields, "network")

		bannedKeys := []string{"nictype", "parent", "mtu", "vlan", "maas.subnet.ipv4", "maas.subnet.ipv6", "gvrp"}
		for _, bannedKey := range bannedKeys {
			if d.config[bannedKey] != "" {
				return fmt.Errorf("Cannot use %q property in conjunction with %q property", bannedKey, "network")
			}
		}

		// If network property is specified, lookup network settings and apply them to the device's config.
		// project.Default is used here as macvlan networks don't suppprt projects.
		n, err := network.LoadByName(d.state, project.Default, d.config["network"])
		if err != nil {
			return errors.Wrapf(err, "Error loading network config for %q", d.config["network"])
		}

		if n.Status() != api.NetworkStatusCreated {
			return fmt.Errorf("Specified network is not fully created")
		}

		if n.Type() != "macvlan" {
			return fmt.Errorf("Specified network must be of type macvlan")
		}

		netConfig := n.Config()

		// Get actual parent device from network's parent setting.
		d.config["parent"] = netConfig["parent"]

		// Copy certain keys verbatim from the network's settings.
		inheritKeys := []string{"mtu", "vlan", "maas.subnet.ipv4", "maas.subnet.ipv6", "gvrp"}
		for _, inheritKey := range inheritKeys {
			if _, found := netConfig[inheritKey]; found {
				d.config[inheritKey] = netConfig[inheritKey]
			}
		}
	} else {
		// If no network property supplied, then parent property is required.
		requiredFields = append(requiredFields, "parent")
	}

	err := d.config.Validate(nicValidationRules(requiredFields, optionalFields, instConf))
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicMACVLAN) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicMACVLAN) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	// Lock to avoid issues with containers starting in parallel.
	networkCreateSharedDeviceLock.Lock()
	defer networkCreateSharedDeviceLock.Unlock()

	revert := revert.New()
	defer revert.Fail()

	saveData := make(map[string]string)

	// Decide which parent we should use based on VLAN setting.
	actualParentName := network.GetHostDevice(d.config["parent"], d.config["vlan"])

	// Record the temporary device name used for deletion later.
	saveData["host_name"] = network.RandomDevName("mac")

	// Create VLAN parent device if needed.
	statusDev, err := networkCreateVlanDeviceIfNeeded(d.state, d.config["parent"], actualParentName, d.config["vlan"], shared.IsTrue(d.config["gvrp"]))
	if err != nil {
		return nil, err
	}

	// Record whether we created the parent device or not so it can be removed on stop.
	saveData["last_state.created"] = fmt.Sprintf("%t", statusDev != "existing")

	if shared.IsTrue(saveData["last_state.created"]) {
		revert.Add(func() {
			networkRemoveInterfaceIfNeeded(d.state, actualParentName, d.inst, d.config["parent"], d.config["vlan"])
		})
	}

	if d.inst.Type() == instancetype.Container {
		// Create MACVLAN interface.
		macvlan := &ip.Macvlan{
			Link: ip.Link{
				Name:   saveData["host_name"],
				Parent: actualParentName,
			},
			Mode: "bridge",
		}
		err = macvlan.Add()
		if err != nil {
			return nil, err
		}
	} else if d.inst.Type() == instancetype.VM {
		// Create MACVTAP interface.
		macvtap := &ip.Macvtap{
			Macvlan: ip.Macvlan{
				Link: ip.Link{
					Name:   saveData["host_name"],
					Parent: actualParentName,
				},
				Mode: "bridge",
			},
		}
		err = macvtap.Add()
		if err != nil {
			return nil, err
		}
	}

	revert.Add(func() { network.InterfaceRemove(saveData["host_name"]) })

	// Set the MAC address.
	if d.config["hwaddr"] != "" {
		link := &ip.Link{Name: saveData["host_name"]}
		err := link.SetAddress(d.config["hwaddr"])
		if err != nil {
			return nil, fmt.Errorf("Failed to set the MAC address: %s", err)
		}
	}

	// Set the MTU.
	if d.config["mtu"] != "" {
		link := &ip.Link{Name: saveData["host_name"]}
		err := link.SetMTU(d.config["mtu"])
		if err != nil {
			return nil, errors.Wrapf(err, "Failed setting MTU %q on %q", d.config["mtu"], saveData["host_name"])
		}
	}

	if d.inst.Type() == instancetype.VM {
		// Bring the interface up on host side.
		link := &ip.Link{Name: saveData["host_name"]}
		err := link.SetUp()
		if err != nil {
			return nil, fmt.Errorf("Failed to bring up interface %s: %v", saveData["host_name"], err)
		}
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "type", Value: "phys"},
		{Key: "name", Value: d.config["name"]},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: saveData["host_name"]},
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

// Stop is run when the device is removed from the instance.
func (d *nicMACVLAN) Stop() (*deviceConfig.RunConfig, error) {
	v := d.volatileGet()
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
		NetworkInterface: []deviceConfig.RunConfigItem{
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
		err := network.InterfaceRemove(v["host_name"])
		if err != nil {
			errs = append(errs, err)
		}
	}

	// This will delete the parent interface if we created it for VLAN parent.
	if shared.IsTrue(v["last_state.created"]) {
		actualParentName := network.GetHostDevice(d.config["parent"], d.config["vlan"])
		err := networkRemoveInterfaceIfNeeded(d.state, actualParentName, d.inst, d.config["parent"], d.config["vlan"])
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%v", errs)
	}

	return nil
}
