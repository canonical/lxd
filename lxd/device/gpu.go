package device

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/resources"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/validate"
)

const gpuDRIDevPath = "/dev/dri"

// Non-card devices such as {/dev/nvidiactl, /dev/nvidia-uvm, ...}
type nvidiaNonCardDevice struct {
	path  string
	major uint32
	minor uint32
}

type gpu struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *gpu) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	rules := map[string]func(string) error{
		"vendorid":  validate.Optional(validate.IsDeviceID),
		"productid": validate.Optional(validate.IsDeviceID),
		"id":        validate.IsAny,
		"pci":       validate.IsAny,
		"uid":       unixValidUserID,
		"gid":       unixValidUserID,
		"mode":      unixValidOctalFileMode,
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	if d.config["pci"] != "" {
		for _, field := range []string{"id", "productid", "vendorid"} {
			if d.config[field] != "" {
				return fmt.Errorf(`Cannot use %q when when "pci" is set`, field)
			}
		}
	}

	if d.config["id"] != "" {
		for _, field := range []string{"pci", "productid", "vendorid"} {
			if d.config[field] != "" {
				return fmt.Errorf(`Cannot use %q when when "id" is set`, field)
			}
		}
	}

	if instConf.Type() == instancetype.VM {
		for _, field := range []string{"uid", "gid", "mode"} {
			if d.config[field] != "" {
				return fmt.Errorf("Cannot use %q when instannce type is VM", field)
			}
		}
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *gpu) validateEnvironment() error {
	if d.config["pci"] != "" && !shared.PathExists(fmt.Sprintf("/sys/bus/pci/devices/%s", d.config["pci"])) {
		return fmt.Errorf("Invalid PCI address (no device found): %s", d.config["pci"])
	}

	return nil
}

// Start is run when the device is added to the container.
func (d *gpu) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	if d.inst.Type() == instancetype.VM {
		return d.startVM()
	}

	return d.startContainer()
}

// startContainer detects the requested GPU devices and sets up unix-char devices.
// Returns RunConfig populated with mount info required to pass the unix-char devices into the container.
func (d *gpu) startContainer() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}
	gpus, err := resources.GetGPU()
	if err != nil {
		return nil, err
	}

	sawNvidia := false
	found := false

	for _, gpu := range gpus.Cards {
		// Skip any cards that don't match the vendorid, pci or productid settings (if specified).
		if (d.config["vendorid"] != "" && gpu.VendorID != d.config["vendorid"]) ||
			(d.config["pci"] != "" && gpu.PCIAddress != d.config["pci"]) ||
			(d.config["productid"] != "" && gpu.ProductID != d.config["productid"]) {
			continue
		}

		// Setup DRM unix-char devices if present and matches id criteria (or if id not specified).
		if gpu.DRM != nil && (d.config["id"] == "" || fmt.Sprintf("%d", gpu.DRM.ID) == d.config["id"]) {
			found = true

			if gpu.DRM.CardName != "" && gpu.DRM.CardDevice != "" && shared.PathExists(filepath.Join(gpuDRIDevPath, gpu.DRM.CardName)) {
				path := filepath.Join(gpuDRIDevPath, gpu.DRM.CardName)
				major, minor, err := d.deviceNumStringToUint32(gpu.DRM.CardDevice)
				if err != nil {
					return nil, err
				}

				err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, major, minor, path, false, &runConf)
				if err != nil {
					return nil, err
				}
			}

			if gpu.DRM.RenderName != "" && gpu.DRM.RenderDevice != "" && shared.PathExists(filepath.Join(gpuDRIDevPath, gpu.DRM.RenderName)) {
				path := filepath.Join(gpuDRIDevPath, gpu.DRM.RenderName)
				major, minor, err := d.deviceNumStringToUint32(gpu.DRM.RenderDevice)
				if err != nil {
					return nil, err
				}

				err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, major, minor, path, false, &runConf)
				if err != nil {
					return nil, err
				}
			}

			if gpu.DRM.ControlName != "" && gpu.DRM.ControlDevice != "" && shared.PathExists(filepath.Join(gpuDRIDevPath, gpu.DRM.ControlName)) {
				path := filepath.Join(gpuDRIDevPath, gpu.DRM.ControlName)
				major, minor, err := d.deviceNumStringToUint32(gpu.DRM.ControlDevice)
				if err != nil {
					return nil, err
				}

				err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, major, minor, path, false, &runConf)
				if err != nil {
					return nil, err
				}
			}

			// Add Nvidia device if present.
			if gpu.Nvidia != nil && gpu.Nvidia.CardName != "" && gpu.Nvidia.CardDevice != "" && shared.PathExists(filepath.Join("/dev", gpu.Nvidia.CardName)) {
				sawNvidia = true
				path := filepath.Join("/dev", gpu.Nvidia.CardName)
				major, minor, err := d.deviceNumStringToUint32(gpu.Nvidia.CardDevice)
				if err != nil {
					return nil, err
				}

				err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, major, minor, path, false, &runConf)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	// Setup additional unix-char devices for nvidia cards.
	// No need to mount additional nvidia non-card devices as the nvidia.runtime setting will do this for us.
	if sawNvidia {
		instanceConfig := d.inst.ExpandedConfig()
		if !shared.IsTrue(instanceConfig["nvidia.runtime"]) {
			nvidiaDevices, err := d.getNvidiaNonCardDevices()
			if err != nil {
				return nil, err
			}

			for _, dev := range nvidiaDevices {
				prefix := deviceJoinPath("unix", d.name)
				if UnixDeviceExists(d.inst.DevicesPath(), prefix, dev.path) {
					continue
				}

				err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, dev.major, dev.minor, dev.path, false, &runConf)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	if !found {
		return nil, fmt.Errorf("Failed to detect requested GPU device")
	}

	return &runConf, nil
}

// startVM detects the requested GPU devices and related virtual functions and rebinds them to the vfio-pci driver.
func (d *gpu) startVM() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}
	gpus, err := resources.GetGPU()
	if err != nil {
		return nil, err
	}

	saveData := make(map[string]string)
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
	}

	if pciAddress == "" {
		return nil, fmt.Errorf("Failed to detect requested GPU device")
	}

	// Get PCI information about the GPU device.
	devicePath := filepath.Join("/sys/bus/pci/devices", pciAddress)
	pciDev, err := pciParseUeventFile(filepath.Join(devicePath, "uevent"))
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to get PCI device info for GPU %q", pciAddress)
	}

	saveData["last_state.pci.slot.name"] = pciDev.SlotName
	saveData["last_state.pci.driver"] = pciDev.Driver

	err = d.pciDeviceDriverOverrideIOMMU(pciDev, "vfio-pci", false)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to override IOMMU group driver")
	}

	runConf.GPUDevice = append(runConf.GPUDevice,
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

// pciDeviceDriverOverrideIOMMU overrides all functions in the specified device's IOMMU group (if exists) that
// are functions of the device. If IOMMU group doesn't exist, only the device itself is overridden.
// If restore argument is true, then IOMMU VF devices related to the main device have their driver override cleared
// rather than being set to the driverOverride specified. This allows for IOMMU VFs that were using a different
// driver (or no driver) when being overridden are not restored back to the main device's driver.
func (d *gpu) pciDeviceDriverOverrideIOMMU(pciDev pciDevice, driverOverride string, restore bool) error {
	iommuGroupPath := filepath.Join("/sys/bus/pci/devices", pciDev.SlotName, "iommu_group", "devices")

	if shared.PathExists(iommuGroupPath) {
		// Extract parent slot name by removing any virtual function ID.
		parts := strings.SplitN(pciDev.SlotName, ".", 2)
		prefix := parts[0]

		// Iterate the members of the IOMMU group and override any that match the parent slot name prefix.
		err := filepath.Walk(iommuGroupPath, func(path string, _ os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			iommuSlotName := filepath.Base(path) // Virtual function's address is dir name.
			if strings.HasPrefix(iommuSlotName, prefix) {
				iommuPciDev := pciDevice{
					Driver:   pciDev.Driver,
					SlotName: iommuSlotName,
				}

				if iommuSlotName != pciDev.SlotName && restore {
					// We don't know the original driver for VFs, so just remove override.
					err = pciDeviceDriverOverride(iommuPciDev, "")
				} else {
					err = pciDeviceDriverOverride(iommuPciDev, driverOverride)
				}

				if err != nil {
					return err
				}
			}

			return nil
		})
		if err != nil {
			return err
		}
	} else {
		err := pciDeviceDriverOverride(pciDev, driverOverride)
		if err != nil {
			return err
		}
	}

	return nil
}

// Stop is run when the device is removed from the instance.
func (d *gpu) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	if d.inst.Type() == instancetype.Container {
		err := unixDeviceRemove(d.inst.DevicesPath(), "unix", d.name, "", &runConf)
		if err != nil {
			return nil, err
		}
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *gpu) postStop() error {
	defer d.volatileSet(map[string]string{
		"last_state.pci.slot.name": "",
		"last_state.pci.driver":    "",
	})

	v := d.volatileGet()

	if d.inst.Type() == instancetype.Container {
		// Remove host files for this device.
		err := unixDeviceDeleteFiles(d.state, d.inst.DevicesPath(), "unix", d.name, "")
		if err != nil {
			return fmt.Errorf("Failed to delete files for device '%s': %v", d.name, err)
		}
	}

	// If VM physical pass through, unbind from vfio-pci and bind back to host driver.
	if d.inst.Type() == instancetype.VM && v["last_state.pci.slot.name"] != "" {
		pciDev := pciDevice{
			Driver:   "vfio-pci",
			SlotName: v["last_state.pci.slot.name"],
		}

		err := d.pciDeviceDriverOverrideIOMMU(pciDev, v["last_state.pci.driver"], true)
		if err != nil {
			return err
		}
	}

	return nil
}

// deviceNumStringToUint32 converts a device number string (major:minor) into separare major and
// minor uint32s.
func (d *gpu) deviceNumStringToUint32(devNum string) (uint32, uint32, error) {
	devParts := strings.SplitN(devNum, ":", 2)
	tmp, err := strconv.ParseUint(devParts[0], 10, 32)
	if err != nil {
		return 0, 0, err
	}
	major := uint32(tmp)

	tmp, err = strconv.ParseUint(devParts[1], 10, 32)
	if err != nil {
		return 0, 0, err
	}
	minor := uint32(tmp)

	return major, minor, nil
}

// getNvidiaNonCardDevices returns device information about Nvidia non-card devices.
func (d *gpu) getNvidiaNonCardDevices() ([]nvidiaNonCardDevice, error) {
	nvidiaEnts, err := ioutil.ReadDir("/dev")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, err
		}
	}

	regexNvidiaCard, err := regexp.Compile(`^nvidia[0-9]+`)
	if err != nil {
		return nil, err
	}

	nvidiaDevices := []nvidiaNonCardDevice{}

	for _, nvidiaEnt := range nvidiaEnts {
		if !strings.HasPrefix(nvidiaEnt.Name(), "nvidia") {
			continue
		}

		if regexNvidiaCard.MatchString(nvidiaEnt.Name()) {
			continue
		}

		nvidiaPath := filepath.Join("/dev", nvidiaEnt.Name())
		stat := unix.Stat_t{}
		err = unix.Stat(nvidiaPath, &stat)
		if err != nil {
			continue
		}

		tmpNividiaGpu := nvidiaNonCardDevice{
			path:  nvidiaPath,
			major: unix.Major(uint64(stat.Rdev)),
			minor: unix.Minor(uint64(stat.Rdev)),
		}

		nvidiaDevices = append(nvidiaDevices, tmpNividiaGpu)
	}

	return nvidiaDevices, nil
}
