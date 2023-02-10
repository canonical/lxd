package device

import (
	"fmt"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	pcidev "github.com/lxc/lxd/lxd/device/pci"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/ip"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

type nicPhysical struct {
	deviceCommon
	network network.Network // Populated in validateConfig().
}

// CanHotPlug returns whether the device can be managed whilst the instance is running. Returns true.
func (d *nicPhysical) CanHotPlug() bool {
	return true
}

// validateConfig checks the supplied config for correctness.
func (d *nicPhysical) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{}
	optionalFields := []string{
		"parent",
		"name",
		"maas.subnet.ipv4",
		"maas.subnet.ipv6",
		"boot.priority",
		"gvrp",
	}

	if d.config["network"] != "" {
		requiredFields = append(requiredFields, "network")

		bannedKeys := []string{"nictype", "parent", "mtu", "vlan", "maas.subnet.ipv4", "maas.subnet.ipv6", "gvrp"}
		for _, bannedKey := range bannedKeys {
			if d.config[bannedKey] != "" {
				return fmt.Errorf("Cannot use %q property in conjunction with %q property", bannedKey, "network")
			}
		}

		// If network property is specified, lookup network settings and apply them to the device's config.
		// project.Default is used here as physical networks don't support projects.
		var err error
		d.network, err = network.LoadByName(d.state, project.Default, d.config["network"])
		if err != nil {
			return fmt.Errorf("Error loading network config for %q: %w", d.config["network"], err)
		}

		if d.network.Status() != api.NetworkStatusCreated {
			return fmt.Errorf("Specified network is not fully created")
		}

		if d.network.Type() != "physical" {
			return fmt.Errorf("Specified network must be of type physical")
		}

		netConfig := d.network.Config()

		// Get actual parent device from network's parent setting.
		d.config["parent"] = netConfig["parent"]

		// Copy certain keys verbatim from the network's settings.
		inheritKeys := []string{"mtu", "vlan", "maas.subnet.ipv4", "maas.subnet.ipv6", "gvrp"}
		for _, inheritKey := range inheritKeys {
			_, found := netConfig[inheritKey]
			if found {
				d.config[inheritKey] = netConfig[inheritKey]
			}
		}
	} else {
		// If no network property supplied, then parent property is required.
		requiredFields = append(requiredFields, "parent")
	}

	if instConf.Type() == instancetype.Container || instConf.Type() == instancetype.Any {
		optionalFields = append(optionalFields, "mtu", "hwaddr", "vlan")
	}

	err := d.config.Validate(nicValidationRules(requiredFields, optionalFields, instConf))
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicPhysical) validateEnvironment() error {
	if d.inst.Type() == instancetype.VM && shared.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
		return fmt.Errorf("Network physical devices cannot be used when migration.stateful is enabled")
	}

	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicPhysical) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	// Lock to avoid issues with containers starting in parallel.
	networkCreateSharedDeviceLock.Lock()
	defer networkCreateSharedDeviceLock.Unlock()

	saveData := make(map[string]string)

	revert := revert.New()
	defer revert.Fail()

	// pciIOMMUGroup, used for VM physical passthrough.
	var pciIOMMUGroup uint64

	// If VM, then try and load the vfio-pci module first.
	if d.inst.Type() == instancetype.VM {
		err = util.LoadModule("vfio-pci")
		if err != nil {
			return nil, fmt.Errorf("Error loading %q module: %w", "vfio-pci", err)
		}
	}

	// Record the host_name device used for restoration later.
	saveData["host_name"] = network.GetHostDevice(d.config["parent"], d.config["vlan"])

	if d.inst.Type() == instancetype.Container {
		statusDev, err := networkCreateVlanDeviceIfNeeded(d.state, d.config["parent"], saveData["host_name"], d.config["vlan"], shared.IsTrue(d.config["gvrp"]))
		if err != nil {
			return nil, err
		}

		// Record whether we created this device or not so it can be removed on stop.
		saveData["last_state.created"] = fmt.Sprintf("%t", statusDev != "existing")

		if shared.IsTrue(saveData["last_state.created"]) {
			revert.Add(func() {
				_ = networkRemoveInterfaceIfNeeded(d.state, saveData["host_name"], d.inst, d.config["parent"], d.config["vlan"])
			})
		}

		// If we didn't create the device we should track various properties so we can restore them when the
		// instance is stopped or the device is detached.
		if shared.IsFalse(saveData["last_state.created"]) {
			err = networkSnapshotPhysicalNIC(saveData["host_name"], saveData)
			if err != nil {
				return nil, err
			}
		}

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
				return nil, fmt.Errorf("Failed setting MTU %q on %q: %w", d.config["mtu"], saveData["host_name"], err)
			}
		}
	} else if d.inst.Type() == instancetype.VM {
		// Get PCI information about the network interface.
		ueventPath := fmt.Sprintf("/sys/class/net/%s/device/uevent", saveData["host_name"])
		pciDev, err := pcidev.ParseUeventFile(ueventPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to get PCI device info for %q: %w", saveData["host_name"], err)
		}

		saveData["last_state.pci.slot.name"] = pciDev.SlotName
		saveData["last_state.pci.driver"] = pciDev.Driver

		pciIOMMUGroup, err = pcidev.DeviceIOMMUGroup(saveData["last_state.pci.slot.name"])
		if err != nil {
			return nil, err
		}

		err = pcidev.DeviceDriverOverride(pciDev, "vfio-pci")
		if err != nil {
			return nil, err
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
				{Key: "pciSlotName", Value: saveData["last_state.pci.slot.name"]},
				{Key: "pciIOMMUGroup", Value: fmt.Sprintf("%d", pciIOMMUGroup)},
			}...)
	}

	revert.Success()
	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *nicPhysical) Stop() (*deviceConfig.RunConfig, error) {
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
func (d *nicPhysical) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"host_name":                "",
			"last_state.hwaddr":        "",
			"last_state.mtu":           "",
			"last_state.created":       "",
			"last_state.pci.slot.name": "",
			"last_state.pci.driver":    "",
		})
	}()

	v := d.volatileGet()

	// If VM physical pass through, unbind from vfio-pci and bind back to host driver.
	if d.inst.Type() == instancetype.VM && v["last_state.pci.slot.name"] != "" {
		vfioDev := pcidev.Device{
			Driver:   "vfio-pci",
			SlotName: v["last_state.pci.slot.name"],
		}

		err := pcidev.DeviceDriverOverride(vfioDev, v["last_state.pci.driver"])
		if err != nil {
			return err
		}
	} else if d.inst.Type() == instancetype.Container {
		hostName := network.GetHostDevice(d.config["parent"], d.config["vlan"])

		// This will delete the parent interface if we created it for VLAN parent.
		if shared.IsTrue(v["last_state.created"]) {
			err := networkRemoveInterfaceIfNeeded(d.state, hostName, d.inst, d.config["parent"], d.config["vlan"])
			if err != nil {
				return err
			}
		} else if v["last_state.pci.slot.name"] == "" {
			err := networkRestorePhysicalNIC(hostName, v)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
