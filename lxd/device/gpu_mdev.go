package device

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/pborman/uuid"
	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	pcidev "github.com/lxc/lxd/lxd/device/pci"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/resources"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

type gpuMdev struct {
	deviceCommon
}

// Start is run when the device is added to the container.
func (d *gpuMdev) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	return d.startVM()
}

// Stop is run when the device is removed from the instance.
func (d *gpuMdev) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// startVM detects the requested GPU devices and related virtual functions and rebinds them to the vfio-pci driver.
func (d *gpuMdev) startVM() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}

	// Get any existing UUID.
	v := d.volatileGet()
	mdevUUID := v["vgpu.uuid"]

	// Get the local GPUs.
	gpus, err := resources.GetGPU()
	if err != nil {
		return nil, err
	}

	var pciAddress string
	for _, gpu := range gpus.Cards {
		// Skip any cards that don't match the vendorid, pci, productid or DRM ID settings (if specified).
		if (d.config["vendorid"] != "" && gpu.VendorID != d.config["vendorid"]) ||
			(d.config["pci"] != "" && gpu.PCIAddress != d.config["pci"]) ||
			(d.config["productid"] != "" && gpu.ProductID != d.config["productid"]) ||
			(d.config["id"] != "" && (gpu.DRM == nil || fmt.Sprintf("%d", gpu.DRM.ID) != d.config["id"])) {
			continue
		}

		if pciAddress != "" {
			return nil, fmt.Errorf("VMs cannot match multiple GPUs per device")
		}

		pciAddress = gpu.PCIAddress

		// Look for the requested mdev profile on the GPU itself.
		mdevFound := false
		mdevAvailable := false
		for k, v := range gpu.Mdev {
			if d.config["mdev"] == k {
				mdevFound = true
				if v.Available > 0 {
					mdevAvailable = true
				}
				break
			}
		}

		// If no mdev found on the GPU and SR-IOV is present, look on the VFs.
		if !mdevFound && gpu.SRIOV != nil {
			for _, vf := range gpu.SRIOV.VFs {
				for k, v := range vf.Mdev {
					if d.config["mdev"] == k {
						mdevFound = true
						if v.Available > 0 {
							mdevAvailable = true

							// Replace the PCI address with that of the VF.
							pciAddress = vf.PCIAddress
						}

						break
					}
				}

				if mdevAvailable {
					break
				}
			}
		}

		if !mdevFound {
			return nil, fmt.Errorf("Invalid mdev profile %q", d.config["mdev"])
		}

		if !mdevAvailable {
			return nil, fmt.Errorf("No available mdev for profile %q", d.config["mdev"])
		}

		// Create the vGPU.
		if mdevUUID == "" || !shared.PathExists(fmt.Sprintf("/sys/bus/pci/devices/%s/%s", pciAddress, mdevUUID)) {
			mdevUUID := uuid.NewRandom().String()

			err = ioutil.WriteFile(filepath.Join(fmt.Sprintf("/sys/bus/pci/devices/%s/mdev_supported_types/%s/create", pciAddress, d.config["mdev"])), []byte(mdevUUID), 200)
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("The requested profile %q does not exist", d.config["mdev"])
				}

				return nil, errors.Wrapf(err, "Failed to create virtual gpu %q", mdevUUID)
			}
		}
	}

	if pciAddress == "" {
		return nil, fmt.Errorf("Failed to detect requested GPU device")
	}

	// Get PCI information about the GPU device.
	devicePath := filepath.Join("/sys/bus/pci/devices", pciAddress)
	pciDev, err := pcidev.ParseUeventFile(filepath.Join(devicePath, "uevent"))
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to get PCI device info for GPU %q", pciAddress)
	}

	// Prepare the new volatile keys.
	saveData := make(map[string]string)
	saveData["last_state.pci.slot.name"] = pciDev.SlotName
	saveData["last_state.pci.driver"] = pciDev.Driver
	saveData["vgpu.uuid"] = mdevUUID

	runConf.GPUDevice = append(runConf.GPUDevice,
		[]deviceConfig.RunConfigItem{
			{Key: "devName", Value: d.name},
			{Key: "pciSlotName", Value: saveData["last_state.pci.slot.name"]},
			{Key: "vgpu", Value: mdevUUID},
		}...)

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *gpuMdev) postStop() error {
	defer d.volatileSet(map[string]string{
		"last_state.pci.slot.name": "",
		"last_state.pci.driver":    "",
		"vgpu.uuid":                "",
	})

	v := d.volatileGet()

	if v["vgpu.uuid"] != "" {
		path := fmt.Sprintf("/sys/bus/mdev/devices/%s", v["vgpu.uuid"])

		if shared.PathExists(path) {
			err := ioutil.WriteFile(filepath.Join(path, "remove"), []byte("1\n"), 0200)
			if err != nil {
				logger.Debugf("Failed to remove vgpu %q", v["vgpu.uuid"])
			}
		}
	}

	return nil
}

// validateConfig checks the supplied config for correctness.
func (d *gpuMdev) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.VM) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{
		"mdev",
	}

	optionalFields := []string{
		"vendorid",
		"productid",
		"id",
		"pci",
	}

	err := d.config.Validate(gpuValidationRules(requiredFields, optionalFields))
	if err != nil {
		return err
	}

	if d.config["pci"] != "" {
		for _, field := range []string{"id", "productid", "vendorid"} {
			if d.config[field] != "" {
				return fmt.Errorf(`Cannot use %q when when "pci" is set`, field)
			}
		}

		d.config["pci"] = pcidev.NormaliseAddress(d.config["pci"])
	}

	if d.config["id"] != "" {
		for _, field := range []string{"pci", "productid", "vendorid"} {
			if d.config[field] != "" {
				return fmt.Errorf(`Cannot use %q when when "id" is set`, field)
			}
		}
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *gpuMdev) validateEnvironment() error {
	if d.inst.Type() == instancetype.VM && shared.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
		return fmt.Errorf("GPU devices cannot be used when migration.stateful is enabled")
	}

	return validatePCIDevice(d.config["pci"])
}
