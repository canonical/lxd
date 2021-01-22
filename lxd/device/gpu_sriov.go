package device

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/device/pci"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/resources"
	"github.com/lxc/lxd/lxd/revert"
)

type gpuSRIOV struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *gpuSRIOV) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.VM) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{}

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

		// PCI devices can be specified as "0000:XX:XX.X" or "XX:XX.X".
		// However, the devices in /sys/bus/pci/devices use the long format which
		// is why we need to make sure the prefix is present.
		if len(d.config["pci"]) == 7 {
			d.config["pci"] = fmt.Sprintf("0000:%s", d.config["pci"])
		}
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
func (d *gpuSRIOV) validateEnvironment() error {
	return validatePCIDevice(d.config)
}

// Start is run when the device is added to the instance.
func (d *gpuSRIOV) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	saveData := make(map[string]string)

	// Get SRIOV parent, i.e. the actual GPU.
	parentPCIAddress, err := d.getParentPCIAddress()
	if err != nil {
		return nil, err
	}

	// Get PCI information about the GPU device.
	devicePath := filepath.Join("/sys/bus/pci/devices", parentPCIAddress)

	pciParentDev, err := pci.ParseUeventFile(filepath.Join(devicePath, "uevent"))
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to get PCI device info for GPU %q", parentPCIAddress)
	}

	vfID, err := d.findFreeVirtualFunction(pciParentDev)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to find free virtual function")
	}

	vfPCIDev, err := d.setupSriovParent(vfID, saveData)
	if err != nil {
		return nil, err
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf.GPUDevice = append(runConf.GPUDevice, []deviceConfig.RunConfigItem{
		{Key: "devName", Value: d.name},
		{Key: "pciSlotName", Value: vfPCIDev.SlotName},
	}...)

	return &runConf, nil
}

// getParentPCIAddress returns the PCI address of the parent GPU.
func (d *gpuSRIOV) getParentPCIAddress() (string, error) {
	gpus, err := resources.GetGPU()
	if err != nil {
		return "", err
	}

	var parentPCIAddress string

	for _, gpu := range gpus.Cards {
		// Skip any cards that don't match the vendorid, pci, productid or DRM ID settings (if specified).
		if (d.config["vendorid"] != "" && gpu.VendorID != d.config["vendorid"]) ||
			(d.config["pci"] != "" && gpu.PCIAddress != d.config["pci"]) ||
			(d.config["productid"] != "" && gpu.ProductID != d.config["productid"]) ||
			(d.config["id"] != "" && (gpu.DRM == nil || fmt.Sprintf("%d", gpu.DRM.ID) != d.config["id"])) {
			continue
		}

		if parentPCIAddress != "" {
			return "", fmt.Errorf("Cannot match multiple GPUs per device")
		}

		parentPCIAddress = gpu.PCIAddress
	}

	if parentPCIAddress == "" {
		return "", fmt.Errorf("Failed to detect requested GPU device")
	}

	return parentPCIAddress, nil
}

// setupSriovParent configures a SR-IOV virtual function (VF) device on parent and stores original properties of
// the physical device into voltatile for restoration on detach. Returns VF PCI device info.
func (d *gpuSRIOV) setupSriovParent(vfID int, volatile map[string]string) (pci.Device, error) {
	revert := revert.New()
	defer revert.Fail()

	volatile["last_state.vf.id"] = fmt.Sprintf("%d", vfID)
	volatile["last_state.created"] = "false" // Indicates don't delete device at stop time.

	parentPCIAddress, err := d.getParentPCIAddress()
	if err != nil {
		return pci.Device{}, err
	}

	// Get VF device's PCI Slot Name so we can unbind and rebind it from the host.
	vfPCIDev, err := d.getVFDevicePCISlot(parentPCIAddress, volatile["last_state.vf.id"])
	if err != nil {
		return vfPCIDev, err
	}

	// Unbind VF device from the host so that the settings will take effect when we rebind it.
	err = pci.DeviceUnbind(vfPCIDev)
	if err != nil {
		return vfPCIDev, err
	}

	revert.Add(func() { pci.DeviceProbe(vfPCIDev) })

	// Register VF device with vfio-pci driver so it can be passed to VM.
	err = pci.DeviceDriverOverride(vfPCIDev, "vfio-pci")
	if err != nil {
		return vfPCIDev, err
	}

	// Record original driver used by VF device for restore.
	volatile["last_state.pci.driver"] = vfPCIDev.Driver

	revert.Success()

	return vfPCIDev, nil
}

// getVFDevicePCISlot returns the PCI slot name for a PCI virtual function device.
func (d *gpuSRIOV) getVFDevicePCISlot(parentPCIAddress string, vfID string) (pci.Device, error) {
	ueventFile := fmt.Sprintf("/sys/bus/pci/devices/%s/virtfn%s/uevent", parentPCIAddress, vfID)
	pciDev, err := pci.ParseUeventFile(ueventFile)
	if err != nil {
		return pciDev, err
	}

	return pciDev, nil
}

func (d *gpuSRIOV) findFreeVirtualFunction(parentDev pci.Device) (int, error) {
	// Get number of currently enabled VFs.
	sriovNumVFs := fmt.Sprintf("/sys/bus/pci/devices/%s/sriov_numvfs", parentDev.SlotName)

	sriovNumVfsBuf, err := ioutil.ReadFile(sriovNumVFs)
	if err != nil {
		return 0, err
	}
	sriovNumVfsStr := strings.TrimSpace(string(sriovNumVfsBuf))
	sriovNum, err := strconv.Atoi(sriovNumVfsStr)
	if err != nil {
		return 0, err
	}

	vfID := -1

	for i := 0; i < sriovNum; i++ {
		pciDev, err := pci.ParseUeventFile(fmt.Sprintf("/sys/bus/pci/devices/%s/virtfn%d/uevent", parentDev.SlotName, i))
		if err != nil {
			return 0, err
		}

		// We assume the virtual function is free if there's no driver bound to it.
		if pciDev.Driver == "" {
			vfID = i
			break
		}
	}

	if vfID == -1 {
		return 0, fmt.Errorf("All virtual functions on parent device %q seem to be in use", parentDev.ID)
	}

	return vfID, nil
}

// Stop is run when the device is removed from the instance.
func (d *gpuSRIOV) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *gpuSRIOV) postStop() error {
	defer d.volatileSet(map[string]string{
		"last_state.created":    "",
		"last_state.vf.id":      "",
		"last_state.pci.driver": "",
	})

	v := d.volatileGet()

	err := d.restoreSriovParent(v)
	if err != nil {
		return err
	}

	return nil
}

// restoreSriovParent restores SR-IOV parent device settings when removed from an instance using the
// volatile data that was stored when the device was first added with setupSriovParent().
func (d *gpuSRIOV) restoreSriovParent(volatile map[string]string) error {
	// Nothing to do if we don't know the original device name or the VF ID.
	if volatile["last_state.vf.id"] == "" || (d.config["pci"] == "" && d.config["id"] == "" && d.config["vendorid"] == "" && d.config["productid"] == "") {
		return nil
	}

	revert := revert.New()
	defer revert.Fail()

	parentPCIAddress, err := d.getParentPCIAddress()
	if err != nil {
		return err
	}

	// Get VF device's PCI info so we can unbind and rebind it from the host.
	vfPCIDev, err := d.getVFDevicePCISlot(parentPCIAddress, volatile["last_state.vf.id"])
	if err != nil {
		return err
	}

	// Unbind VF device from the host so that the restored settings will take effect when we rebind it.
	err = pci.DeviceUnbind(vfPCIDev)
	if err != nil {
		return err
	}

	if d.inst.Type() == instancetype.VM {
		// Before we bind the device back to the host, ensure we restore the original driver info as it
		// should be currently set to vfio-pci.
		err = pci.DeviceSetDriverOverride(vfPCIDev, volatile["last_state.pci.driver"])
		if err != nil {
			return err
		}
	}

	// However we return from this function, we must try to rebind the VF so its not orphaned.
	// The OS won't let an already bound device be bound again so is safe to call twice.
	revert.Add(func() { pci.DeviceProbe(vfPCIDev) })

	// Bind VF device onto the host so that the settings will take effect.
	err = pci.DeviceProbe(vfPCIDev)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}
