package device

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/device/cdi"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	pcidev "github.com/canonical/lxd/lxd/device/pci"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

const gpuDRIDevPath = "/dev/dri"

// Non-card devices such as {/dev/nvidiactl, /dev/nvidia-uvm, ...}.
type nvidiaNonCardDevice struct {
	path  string
	major uint32
	minor uint32
}

type gpuPhysical struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *gpuPhysical) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	optionalFields := []string{
		"vendorid",
		"productid",
		"id",
		"pci",
	}

	if instConf.Type() == instancetype.Container || instConf.Type() == instancetype.Any {
		optionalFields = append(optionalFields, "uid", "gid", "mode")
	}

	err := d.config.Validate(gpuValidationRules(nil, optionalFields))
	if err != nil {
		return err
	}

	if d.config["pci"] != "" {
		for _, field := range []string{"id", "productid", "vendorid"} {
			if d.config[field] != "" {
				return fmt.Errorf(`Cannot use %q when "pci" is set`, field)
			}
		}

		d.config["pci"] = pcidev.NormaliseAddress(d.config["pci"])
	}

	if d.config["id"] != "" {
		for _, field := range []string{"pci", "productid", "vendorid"} {
			if d.config[field] != "" {
				return fmt.Errorf(`Cannot use %q when "id" is set`, field)
			}
		}

		// Validate id is either integer DRM ID or CDI ID.
		_, err = strconv.Atoi(d.config["id"])
		if err != nil {
			_, err := cdi.ToCDI(d.config["id"])
			if err != nil {
				// Structurally incorrect CDI ID supplied.
				if api.StatusErrorCheck(err, http.StatusBadRequest) {
					return fmt.Errorf("ID must be integer DRM ID or CDI ID: %w", err)
				}

				// Structurally correct CDI ID supplied, but still invalid for some reason.
				return err
			}
		}
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *gpuPhysical) validateEnvironment() error {
	if d.inst.Type() == instancetype.VM && shared.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
		return errors.New("GPU devices cannot be used when migration.stateful is enabled")
	}

	return validatePCIDevice(d.config["pci"])
}

// Start is run when the device is added to the container.
func (d *gpuPhysical) Start() (*deviceConfig.RunConfig, error) {
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
func (d *gpuPhysical) startContainer() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}
	if d.config["id"] != "" {
		// Check if the id of the device match a CDI format.
		// The cdiID can be nil if the provided ID doesn't conform to the CDI (Container Device Interface)
		// format and this will not be treated as an error, as we allow the program to continue processing.
		// The ID might still be valid in other contexts, such as a DRM card ID.
		// This flexibility allows for both CDI-compliant device specifications and legacy device IDs.
		cdiID, _ := cdi.ToCDI(d.config["id"])
		if cdiID != nil {
			if cdiID.Class == cdi.MIG {
				return nil, errors.New(`MIG GPU notation detected for a "physical" gputype device. Choose a "mig" gputype device instead.`)
			}

			err := applyCDIDeviceToContainer(&d.deviceCommon, *cdiID, &runConf)
			if err != nil {
				return nil, err
			}

			return &runConf, nil
		}
	}

	// If we use a non-CDI approach, we proceeds with the normal GPU detection approach using the provided DRM card id
	// or PCI-e bus address.
	gpus, err := resources.GetGPU()
	if err != nil {
		return nil, err
	}

	sawNvidia := false
	found := false

	for _, gpu := range gpus.Cards {
		// Skip any cards that are not selected.
		if !gpuSelected(d.Config(), gpu) {
			continue
		}

		// We found a match.
		found = true

		// Setup DRM unix-char devices if present.
		if gpu.DRM != nil {
			if gpu.DRM.CardName != "" && gpu.DRM.CardDevice != "" {
				path := filepath.Join(gpuDRIDevPath, gpu.DRM.CardName)
				if shared.PathExists(path) {
					major, minor, err := d.deviceNumStringToUint32(gpu.DRM.CardDevice)
					if err != nil {
						return nil, err
					}

					err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, major, minor, path, false, &runConf)
					if err != nil {
						return nil, err
					}
				}
			}

			if gpu.DRM.RenderName != "" && gpu.DRM.RenderDevice != "" {
				path := filepath.Join(gpuDRIDevPath, gpu.DRM.RenderName)
				if shared.PathExists(path) {
					major, minor, err := d.deviceNumStringToUint32(gpu.DRM.RenderDevice)
					if err != nil {
						return nil, err
					}

					err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, major, minor, path, false, &runConf)
					if err != nil {
						return nil, err
					}
				}
			}

			if gpu.DRM.ControlName != "" && gpu.DRM.ControlDevice != "" {
				path := filepath.Join(gpuDRIDevPath, gpu.DRM.ControlName)
				if shared.PathExists(path) {
					major, minor, err := d.deviceNumStringToUint32(gpu.DRM.ControlDevice)
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

		// Add Nvidia device if present.
		if gpu.Nvidia != nil && gpu.Nvidia.CardName != "" && gpu.Nvidia.CardDevice != "" {
			path := filepath.Join("/dev", gpu.Nvidia.CardName)
			if shared.PathExists(path) {
				sawNvidia = true
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
	if sawNvidia {
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

	if !found {
		return nil, errors.New("Failed detecting requested GPU device")
	}

	return &runConf, nil
}

// startVM detects the requested GPU devices and related virtual functions and rebinds them to the vfio-pci driver.
func (d *gpuPhysical) startVM() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}
	gpus, err := resources.GetGPU()
	if err != nil {
		return nil, err
	}

	saveData := make(map[string]string)
	var pciAddress string

	for _, gpu := range gpus.Cards {
		// Skip any cards that are not selected.
		if !gpuSelected(d.Config(), gpu) {
			continue
		}

		// Check for existing running processes tied to the GPU.
		// Failing early here in case of attached running processes to the card
		// avoids a blocking call to os.WriteFile() when unbinding the device.
		if gpu.Nvidia != nil && gpu.Nvidia.CardName != "" {
			devPath := filepath.Join("/dev", gpu.Nvidia.CardName)
			if !shared.PathExists(devPath) {
				continue
			}

			runningProcs, err := checkAttachedRunningProcesses(devPath)
			if err != nil {
				return nil, err
			}

			if len(runningProcs) > 0 {
				return nil, fmt.Errorf(
					"Cannot use device %q, %d processes are still attached to it:\n\t%s",
					devPath, len(runningProcs), strings.Join(runningProcs, "\n\t"),
				)
			}
		}

		if pciAddress != "" {
			return nil, errors.New("VMs cannot match multiple GPUs per device")
		}

		pciAddress = gpu.PCIAddress
	}

	if pciAddress == "" {
		return nil, errors.New("Failed detecting requested GPU device")
	}

	// Make sure that vfio-pci is loaded.
	err = util.LoadModule("vfio-pci")
	if err != nil {
		return nil, fmt.Errorf("Error loading %q module: %w", "vfio-pci", err)
	}

	// Get PCI information about the GPU device.
	pciDev, err := pcidev.ParseUeventFile(filepath.Join("/sys/bus/pci/devices", pciAddress, "uevent"))
	if err != nil {
		return nil, fmt.Errorf("Failed getting PCI device info for GPU %q: %w", pciAddress, err)
	}

	saveData["last_state.pci.slot.name"] = pciDev.SlotName
	saveData["last_state.pci.driver"] = pciDev.Driver

	err = d.pciDeviceDriverOverrideIOMMU(pciDev, "vfio-pci", false)
	if err != nil {
		return nil, fmt.Errorf("Failed overriding IOMMU group driver: %w", err)
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
func (d *gpuPhysical) pciDeviceDriverOverrideIOMMU(pciDev pcidev.Device, driverOverride string, restore bool) error {
	iommuGroupPath := filepath.Join("/sys/bus/pci/devices", pciDev.SlotName, "iommu_group", "devices")

	if shared.PathExists(iommuGroupPath) {
		// Extract parent slot name by removing any virtual function ID.
		prefix, _, _ := strings.Cut(pciDev.SlotName, ".")

		// Iterate the members of the IOMMU group and override any that match the parent slot name prefix.
		err := filepath.Walk(iommuGroupPath, func(path string, _ os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			iommuSlotName := filepath.Base(path) // Virtual function's address is dir name.
			if !strings.HasPrefix(iommuSlotName, prefix) {
				return nil
			}

			iommuPciDev := pcidev.Device{
				Driver:   pciDev.Driver,
				SlotName: iommuSlotName,
			}

			if iommuSlotName != pciDev.SlotName && restore {
				// We don't know the original driver for VFs, so just remove override.
				return pcidev.DeviceDriverOverride(iommuPciDev, "")
			}

			return pcidev.DeviceDriverOverride(iommuPciDev, driverOverride)
		})
		if err != nil {
			return err
		}
	} else {
		err := pcidev.DeviceDriverOverride(pciDev, driverOverride)
		if err != nil {
			return err
		}
	}

	return nil
}

// CanHotPlug returns whether the device can be managed whilst the instance is running.
// Both CDI and classic GPU devices can be hotplugged for containers.
func (d *gpuPhysical) CanHotPlug() bool {
	return d.inst.Type() == instancetype.Container
}

// Stop is run when the device is removed from the instance.
func (d *gpuPhysical) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	if d.inst.Type() != instancetype.Container {
		return &runConf, nil
	}

	cdiID, _ := cdi.ToCDI(d.config["id"])
	if cdiID != nil {
		// This is more efficient than GenerateFromCDI as we don't need to re-generate a CDI
		// specification to parse it again.
		configDevices, err := cdi.ReloadConfigDevicesFromDisk(cdiConfigDevicesFilePath(d.inst.DevicesPath(), d.name))
		if err != nil {
			return nil, err
		}

		err = stopCDIDevices(&d.deviceCommon, configDevices, &runConf)
		if err != nil {
			return nil, err
		}

		return &runConf, nil
	}

	// In case of an 'id' not being CDI-compliant (e.g, a legacy DRM card id),
	// we remove unix devices only as usual.
	err := unixDeviceRemove(d.inst.DevicesPath(), "unix", d.name, "", &runConf)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *gpuPhysical) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"last_state.pci.slot.name": "",
			"last_state.pci.driver":    "",
			"vgpu.uuid":                "",
		})
	}()

	v := d.volatileGet()

	if d.inst.Type() == instancetype.Container {
		cdiID, _ := cdi.ToCDI(d.config["id"])
		if cdiID != nil {
			err := postStopCDIDevice(&d.deviceCommon, false)
			if err != nil {
				return err
			}
		} else {
			err := unixDeviceDeleteFiles(d.state, d.inst.DevicesPath(), "unix", d.name, "")
			if err != nil {
				return fmt.Errorf("Failed deleting files for device %q: %w", d.name, err)
			}
		}

		return nil
	}

	// If VM physical pass through, unbind from vfio-pci and bind back to host driver.
	if d.inst.Type() == instancetype.VM && v["last_state.pci.slot.name"] != "" {
		pciDev := pcidev.Device{
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
func (d *gpuPhysical) deviceNumStringToUint32(devNum string) (major uint32, minor uint32, err error) {
	devParts := strings.SplitN(devNum, ":", 2)
	tmp, err := strconv.ParseUint(devParts[0], 10, 32)
	if err != nil {
		return 0, 0, err
	}

	major = uint32(tmp)

	tmp, err = strconv.ParseUint(devParts[1], 10, 32)
	if err != nil {
		return 0, 0, err
	}

	minor = uint32(tmp)

	return major, minor, nil
}

// getNvidiaNonCardDevices returns device information about Nvidia non-card devices.
func (d *gpuPhysical) getNvidiaNonCardDevices() ([]nvidiaNonCardDevice, error) {
	nvidiaEnts, err := os.ReadDir("/dev")
	if err != nil {
		return nil, err
	}

	nvidiaDevices := []nvidiaNonCardDevice{}

	for _, nvidiaEnt := range nvidiaEnts {
		nvidiaEntName := nvidiaEnt.Name()
		if !strings.HasPrefix(nvidiaEntName, "nvidia") {
			continue
		}

		// Skip the nvidia directories for now (require extra MIG support).
		if nvidiaEnt.IsDir() {
			continue
		}

		// Skip card devices (nvidia0, nvidia1, ...) identified by a numeric suffix.
		_, err := strconv.Atoi(strings.TrimPrefix(nvidiaEntName, "nvidia"))
		if err == nil {
			continue
		}

		nvidiaPath := filepath.Join("/dev", nvidiaEntName)
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
