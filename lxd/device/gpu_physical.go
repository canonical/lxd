package device

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/device/cdi"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	pcidev "github.com/canonical/lxd/lxd/device/pci"
	"github.com/canonical/lxd/lxd/idmap"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
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
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *gpuPhysical) validateEnvironment() error {
	if d.inst.Type() == instancetype.VM && shared.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
		return fmt.Errorf("GPU devices cannot be used when migration.stateful is enabled")
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

// startCDIDevices starts all the devices given in a CDI specification:
// * `unix-char` (representing the card and non-card devices)
// * `disk` (representing the mounts)).
func (d *gpuPhysical) startCDIDevices(configDevices cdi.ConfigDevices, runConf *deviceConfig.RunConfig) error {
	srcFDHandlers := make([]*os.File, 0)
	defer func() {
		for _, f := range srcFDHandlers {
			_ = f.Close()
		}
	}()

	for _, conf := range configDevices.UnixCharDevs {
		if conf["source"] == "" {
			return fmt.Errorf("The source of the unix-char device %v used for CDI is empty", conf)
		}

		if conf["major"] == "" || conf["minor"] == "" {
			return fmt.Errorf("The major or minor of the unix-char device %v used for CDI is empty", conf)
		}

		major, err := strconv.ParseUint(conf["major"], 10, 32)
		if err != nil {
			return fmt.Errorf("Failed to parse major number %q when starting CDI device: %w", conf["major"], err)
		}

		minor, err := strconv.ParseUint(conf["minor"], 10, 32)
		if err != nil {
			return fmt.Errorf("Failed to parse minor number %q when starting CDI device: %w", conf["minor"], err)
		}

		// Here putting a `cdi.CDIUnixPrefix` prefix with 'd.name' as a device name will create an directory entry like:
		// <lxd_var_path>/devices/<instance_name>/<cdi.CDIUnixPrefix>.<gpu_device_name>.<path_encoded_relative_dest_path>
		// 'unixDeviceSetupCharNum' is already checking for dupe entries so we have no validation to do here.
		err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), cdi.CDIUnixPrefix, d.name, conf, uint32(major), uint32(minor), conf["path"], false, runConf)
		if err != nil {
			return err
		}
	}

	// Create the devices directory if missing.
	if !shared.PathExists(d.inst.DevicesPath()) {
		err := os.Mkdir(d.inst.DevicesPath(), 0711)
		if err != nil {
			return err
		}
	}

	for _, conf := range configDevices.BindMounts {
		if conf["source"] == "" {
			return fmt.Errorf("The source of the disk device %v used for CDI is empty", conf)
		}

		srcPath := shared.HostPath(conf["source"])
		destPath := conf["path"]
		relativeDestPath := strings.TrimPrefix(destPath, "/")

		// This time, the created path will be like:
		// <lxd_var_path>/devices/<instance_name>/<cdi.CDIDiskPrefix>.<gpu_device_name>.<path_encoded_relative_dest_path>
		deviceName := filesystem.PathNameEncode(deviceJoinPath(cdi.CDIDiskPrefix, d.name, relativeDestPath))
		devPath := filepath.Join(d.inst.DevicesPath(), deviceName)

		ownerShift := deviceConfig.MountOwnerShiftNone
		if idmap.CanIdmapMount(devPath, "") {
			ownerShift = deviceConfig.MountOwnerShiftDynamic
		}

		options := []string{"bind"}
		mntOptions := shared.SplitNTrimSpace(conf["raw.mount.options"], ",", -1, true)
		fsName := "none"

		fileInfo, err := os.Stat(srcPath)
		if err != nil {
			return fmt.Errorf("Failed accessing source path %q: %w", srcPath, err)
		}

		fileMode := fileInfo.Mode()
		isFile := false
		if !fileMode.IsDir() {
			isFile = true
		}

		f, err := os.OpenFile(srcPath, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("Failed opening source path %q: %w", srcPath, err)
		}

		srcPath = fmt.Sprintf("/proc/self/fd/%d", f.Fd())
		srcFDHandlers = append(srcFDHandlers, f)

		// Clean any existing entry.
		if shared.PathExists(devPath) {
			err := os.Remove(devPath)
			if err != nil {
				return err
			}
		}

		// Create the mount point.
		if isFile {
			f, err := os.Create(devPath)
			if err != nil {
				return err
			}

			srcFDHandlers = append(srcFDHandlers, f)
		} else {
			err := os.Mkdir(devPath, 0700)
			if err != nil {
				return err
			}
		}

		// Mount the fs.
		err = DiskMount(srcPath, devPath, false, "", mntOptions, fsName)
		if err != nil {
			return err
		}

		if isFile {
			options = append(options, "create=file")
		} else {
			options = append(options, "create=dir")
		}

		runConf.Mounts = append(runConf.Mounts, deviceConfig.MountEntryItem{
			DevName:    deviceName,
			DevPath:    devPath,
			TargetPath: relativeDestPath,
			FSType:     "none",
			Opts:       options,
			OwnerShift: ownerShift,
		})

		runConf.PostHooks = append(runConf.PostHooks, func() error {
			err := unix.Unmount(devPath, unix.MNT_DETACH)
			if err != nil {
				return err
			}

			return nil
		})
	}

	// Serialize the config devices inside the devices directory.
	f, err := os.Create(d.generateCDIConfigDevicesFilePath())
	if err != nil {
		return fmt.Errorf("Could not create the CDI config devices file: %w", err)
	}

	defer f.Close()
	err = json.NewEncoder(f).Encode(configDevices)
	if err != nil {
		return fmt.Errorf("Could not write to the CDI config devices file: %w", err)
	}

	return nil
}

func (d *gpuPhysical) generateCDIHooksFilePath() string {
	return filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("%s%s", d.name, cdi.CDIHooksFileSuffix))
}

func (d *gpuPhysical) generateCDIConfigDevicesFilePath() string {
	return filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("%s%s", d.name, cdi.CDIConfigDevicesFileSuffix))
}

// startContainer detects the requested GPU devices and sets up unix-char devices.
// Returns RunConfig populated with mount info required to pass the unix-char devices into the container.
func (d *gpuPhysical) startContainer() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}
	if d.config["id"] != "" {
		// Check if the id of the device match a CDI format.
		cdiID, err := cdi.ToCDI(d.config["id"])
		if err != nil {
			return nil, err
		}

		// The cdiID can be empty if the provided ID doesn't conform to the CDI (Container Device Interface) format,
		// and this will not be treated as an error, as we allow the program to continue processing.
		// The ID might still be valid in other contexts, such as a DRM card ID.
		// This flexibility allows for both CDI-compliant device specifications and legacy device identifiers.
		if !cdiID.Empty() {
			if cdiID.Class == cdi.MIG {
				return nil, fmt.Errorf(`MIG GPU notation detected for a "physical" gputype device. Choose a "mig" gputype device instead.`)
			}

			configDevices, hooks, err := cdi.GenerateFromCDI(d.inst, cdiID, d.logger)
			if err != nil {
				return nil, err
			}

			// Start the devices needed by the CDI specification.
			err = d.startCDIDevices(*configDevices, &runConf)
			if err != nil {
				return nil, err
			}

			// Persist the hooks to be run on a `lxc.hook.mount` LXC hook.
			hooksFile := d.generateCDIHooksFilePath()
			f, err := os.Create(hooksFile)
			if err != nil {
				return nil, fmt.Errorf("Could not create the CDI hooks file: %w", err)
			}

			defer f.Close()
			err = json.NewEncoder(f).Encode(hooks)
			if err != nil {
				return nil, fmt.Errorf("Could not write to the CDI hooks file: %w", err)
			}

			runConf.GPUDevice = append(runConf.GPUDevice,
				[]deviceConfig.RunConfigItem{
					{Key: cdi.CDIHookDefinitionKey, Value: filepath.Base(hooksFile)},
				}...)

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

	// Setup additional unix-char devices for nvidia cards.
	// No need to mount additional nvidia non-card devices as the nvidia.runtime setting will do this for us.
	if sawNvidia {
		instanceConfig := d.inst.ExpandedConfig()
		if shared.IsFalseOrEmpty(instanceConfig["nvidia.runtime"]) {
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
		if gpu.Nvidia != nil && gpu.Nvidia.CardName != "" && shared.PathExists(filepath.Join("/dev", gpu.Nvidia.CardName)) {
			devPath := filepath.Join("/dev", gpu.Nvidia.CardName)
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
			return nil, fmt.Errorf("VMs cannot match multiple GPUs per device")
		}

		pciAddress = gpu.PCIAddress
	}

	if pciAddress == "" {
		return nil, fmt.Errorf("Failed to detect requested GPU device")
	}

	// Make sure that vfio-pci is loaded.
	err = util.LoadModule("vfio-pci")
	if err != nil {
		return nil, fmt.Errorf("Error loading %q module: %w", "vfio-pci", err)
	}

	// Get PCI information about the GPU device.
	devicePath := filepath.Join("/sys/bus/pci/devices", pciAddress)
	pciDev, err := pcidev.ParseUeventFile(filepath.Join(devicePath, "uevent"))
	if err != nil {
		return nil, fmt.Errorf("Failed to get PCI device info for GPU %q: %w", pciAddress, err)
	}

	saveData["last_state.pci.slot.name"] = pciDev.SlotName
	saveData["last_state.pci.driver"] = pciDev.Driver

	err = d.pciDeviceDriverOverrideIOMMU(pciDev, "vfio-pci", false)
	if err != nil {
		return nil, fmt.Errorf("Failed to override IOMMU group driver: %w", err)
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
		parts := strings.SplitN(pciDev.SlotName, ".", 2)
		prefix := parts[0]

		// Iterate the members of the IOMMU group and override any that match the parent slot name prefix.
		err := filepath.Walk(iommuGroupPath, func(path string, _ os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			iommuSlotName := filepath.Base(path) // Virtual function's address is dir name.
			if strings.HasPrefix(iommuSlotName, prefix) {
				iommuPciDev := pcidev.Device{
					Driver:   pciDev.Driver,
					SlotName: iommuSlotName,
				}

				if iommuSlotName != pciDev.SlotName && restore {
					// We don't know the original driver for VFs, so just remove override.
					err = pcidev.DeviceDriverOverride(iommuPciDev, "")
				} else {
					err = pcidev.DeviceDriverOverride(iommuPciDev, driverOverride)
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
		err := pcidev.DeviceDriverOverride(pciDev, driverOverride)
		if err != nil {
			return err
		}
	}

	return nil
}

// stopCDIDevices reads the configDevices and remove potential unix device and unmounts disk mounts.
func (d *gpuPhysical) stopCDIDevices(configDevices cdi.ConfigDevices, runConf *deviceConfig.RunConfig) error {
	// Remove ALL the underlying unix-char dev entries created when the CDI device started.
	err := unixDeviceRemove(d.inst.DevicesPath(), cdi.CDIUnixPrefix, d.name, "", runConf)
	if err != nil {
		return err
	}

	for _, conf := range configDevices.BindMounts {
		relativeDestPath := strings.TrimPrefix(conf["path"], "/")
		devPath := filepath.Join(d.inst.DevicesPath(), filesystem.PathNameEncode(deviceJoinPath(cdi.CDIDiskPrefix, d.name, relativeDestPath)))
		runConf.PostHooks = append(runConf.PostHooks, func() error {
			// Clean any existing device mount entry. Should occur first before custom volume unmounts.
			err := DiskMountClear(devPath)
			if err != nil {
				return err
			}

			return nil
		})

		// The disk device doesn't exist do nothing.
		if !shared.PathExists(devPath) {
			return nil
		}

		// Request an unmount of the device inside the instance.
		runConf.Mounts = append(runConf.Mounts, deviceConfig.MountEntryItem{
			TargetPath: relativeDestPath,
		})
	}

	return nil
}

// CanHotPlug returns whether the device can be managed whilst the instance is running.
// CDI GPU are not hotpluggable because the configuration of a CDI GPU requires a LXC hook that
// is only run at instance start. A classic GPU device can be hotplugged.
func (d *gpuPhysical) CanHotPlug() bool {
	if d.inst.Type() == instancetype.Container {
		if d.config["id"] != "" {
			// Check if the id of the device match a CDI format.
			cdiID, err := cdi.ToCDI(d.config["id"])
			if err != nil {
				d.logger.Error("Failed to parse CDI ID when hotplugging", logger.Ctx{"err": err})
				return false
			}

			if !cdiID.Empty() {
				d.logger.Warn("Hotplugging CDI devices is not supported", logger.Ctx{"id": d.config["id"]})
				return false
			}
		}

		return true
	}

	return false
}

// Stop is run when the device is removed from the instance.
func (d *gpuPhysical) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	if d.inst.Type() == instancetype.Container {
		cdiID, err := cdi.ToCDI(d.config["id"])
		if err != nil {
			return nil, err
		}

		if !cdiID.Empty() {
			// This is more efficient than GenerateFromCDI as we don't need to re-generate a CDI specification to parse it again.
			configDevices, err := cdi.ReloadConfigDevicesFromDisk(d.generateCDIConfigDevicesFilePath())
			if err != nil {
				return nil, err
			}

			err = d.stopCDIDevices(configDevices, &runConf)
			if err != nil {
				return nil, err
			}

			return &runConf, nil
		}

		// In case of an 'id' not being CDI-compliant (e.g, a legacy DRM card id), we remove unix devices only as usual.
		err = unixDeviceRemove(d.inst.DevicesPath(), "unix", d.name, "", &runConf)
		if err != nil {
			return nil, err
		}
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
		cdiID, err := cdi.ToCDI(d.config["id"])
		if err != nil {
			return err
		}

		if !cdiID.Empty() {
			err = unixDeviceDeleteFiles(d.state, d.inst.DevicesPath(), cdi.CDIUnixPrefix, d.name, "")
			if err != nil {
				return fmt.Errorf("Failed to delete files for CDI device '%s': %w", d.name, err)
			}

			// Also remove the JSON files that were used to store the CDI related information.
			err = os.Remove(d.generateCDIHooksFilePath())
			if err != nil {
				return fmt.Errorf("Failed to delete CDI hooks file for device %q: %w", d.name, err)
			}

			err = os.Remove(d.generateCDIConfigDevicesFilePath())
			if err != nil {
				return fmt.Errorf("Failed to delete CDI paths to conf file for device %q: %w", d.name, err)
			}
		} else {
			err = unixDeviceDeleteFiles(d.state, d.inst.DevicesPath(), "unix", d.name, "")
			if err != nil {
				return fmt.Errorf("Failed to delete files for device %q: %w", d.name, err)
			}
		}

		return err
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

		// Skip the nvidia directories for now (require extra MIG support).
		if nvidiaEnt.IsDir() {
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
