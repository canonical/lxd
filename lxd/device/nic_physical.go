package device

import (
	"fmt"

	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
)

type nicPhysical struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *nicPhysical) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{"parent"}
	optionalFields := []string{
		"name",
		"maas.subnet.ipv4",
		"maas.subnet.ipv6",
		"boot.priority",
	}

	if instConf.Type() == instancetype.Container {
		optionalFields = append(optionalFields, "mtu", "hwaddr", "vlan")
	}

	err := d.config.Validate(nicValidationRules(requiredFields, optionalFields))
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicPhysical) validateEnvironment() error {
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

	// pciSlotName, used for VM physical passthrough.
	var pciSlotName string

	// If VM, then try and load the vfio-pci module first.
	if d.inst.Type() == instancetype.VM {
		err = util.LoadModule("vfio-pci")
		if err != nil {
			return nil, errors.Wrapf(err, "Error loading %q module", "vfio-pci")
		}
	}

	// Record the host_name device used for restoration later.
	saveData["host_name"] = network.GetHostDevice(d.config["parent"], d.config["vlan"])

	if d.inst.Type() == instancetype.Container {
		statusDev, err := networkCreateVlanDeviceIfNeeded(d.state, d.config["parent"], saveData["host_name"], d.config["vlan"])
		if err != nil {
			return nil, err
		}

		// Record whether we created this device or not so it can be removed on stop.
		saveData["last_state.created"] = fmt.Sprintf("%t", statusDev != "existing")

		if shared.IsTrue(saveData["last_state.created"]) {
			revert.Add(func() {
				networkRemoveInterfaceIfNeeded(d.state, saveData["host_name"], d.inst, d.config["parent"], d.config["vlan"])
			})
		}

		// If we didn't create the device we should track various properties so we can restore them when the
		// instance is stopped or the device is detached.
		if !shared.IsTrue(saveData["last_state.created"]) {
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
			err = network.InterfaceSetMTU(saveData["host_name"], d.config["mtu"])
			if err != nil {
				return nil, err
			}
		}
	} else if d.inst.Type() == instancetype.VM {
		// Get PCI information about the network interface.
		ueventPath := fmt.Sprintf("/sys/class/net/%s/device/uevent", saveData["host_name"])
		pciDev, err := pciParseUeventFile(ueventPath)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to get PCI device info for %q", saveData["host_name"])
		}

		saveData["last_state.pci.slot.name"] = pciDev.SlotName
		saveData["last_state.pci.driver"] = pciDev.Driver

		err = pciDeviceDriverOverride(pciDev, "vfio-pci")
		if err != nil {
			return nil, err
		}

		pciSlotName = saveData["last_state.pci.slot.name"]
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
		{Key: "link", Value: saveData["host_name"]},
	}

	if d.inst.Type() == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface,
			[]deviceConfig.RunConfigItem{
				{Key: "devName", Value: d.name},
				{Key: "pciSlotName", Value: pciSlotName},
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
	defer d.volatileSet(map[string]string{
		"host_name":                "",
		"last_state.hwaddr":        "",
		"last_state.mtu":           "",
		"last_state.created":       "",
		"last_state.pci.slot.name": "",
		"last_state.pci.driver":    "",
	})

	v := d.volatileGet()

	// If VM physical pass through, unbind from vfio-pci and bind back to host driver.
	if d.inst.Type() == instancetype.VM && v["last_state.pci.slot.name"] != "" {
		vfioDev := pciDevice{
			Driver:   "vfio-pci",
			SlotName: v["last_state.pci.slot.name"],
		}

		err := pciDeviceDriverOverride(vfioDev, v["last_state.pci.driver"])
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
			err := networkRestorePhysicalNic(hostName, v)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
