package device

import (
	"path/filepath"

	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	pcidev "github.com/lxc/lxd/lxd/device/pci"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/shared/validate"
)

type pci struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *pci) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.VM) {
		return ErrUnsupportedDevType
	}

	rules := map[string]func(string) error{
		"address": validate.IsPCIAddress,
	}

	err := d.config.Validate(rules)
	if err != nil {
		return errors.Wrapf(err, "Failed to validate config")
	}

	d.config["address"] = pcidev.NormaliseAddress(d.config["address"])

	return nil
}

// validateEnvironment checks if the PCI device is available.
func (d *pci) validateEnvironment() error {
	return validatePCIDevice(d.config["address"])
}

// Start is run when the device is added to the instance.
func (d *pci) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to validate environment")
	}

	runConf := deviceConfig.RunConfig{}
	saveData := make(map[string]string)

	// Get PCI information about the device.
	pciAddress := d.config["address"]
	devicePath := filepath.Join("/sys/bus/pci/devices", pciAddress)
	pciDev, err := pcidev.ParseUeventFile(filepath.Join(devicePath, "uevent"))
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to get PCI device info for %q", pciAddress)
	}

	saveData["last_state.pci.slot.name"] = pciDev.SlotName
	saveData["last_state.pci.driver"] = pciDev.Driver

	err = pcidev.DeviceDriverOverride(pciDev, "vfio-pci")
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to override IOMMU group driver")
	}

	runConf.PCIDevice = append(runConf.PCIDevice,
		[]deviceConfig.RunConfigItem{
			{Key: "devName", Value: d.name},
			{Key: "pciSlotName", Value: saveData["last_state.pci.slot.name"]},
		}...)

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *pci) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *pci) postStop() error {
	defer d.volatileSet(map[string]string{
		"last_state.pci.slot.name": "",
		"last_state.pci.driver":    "",
	})

	v := d.volatileGet()

	// Unbind from vfio-pci and bind back to host driver.
	if v["last_state.pci.slot.name"] != "" {
		pciDev := pcidev.Device{
			Driver:   "vfio-pci",
			SlotName: v["last_state.pci.slot.name"],
		}

		err := pcidev.DeviceDriverOverride(pciDev, v["last_state.pci.driver"])
		if err != nil {
			return err
		}
	}

	return nil
}
