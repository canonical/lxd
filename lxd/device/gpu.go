package device

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/resources"
	"github.com/lxc/lxd/shared"
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
func (d *gpu) validateConfig() error {
	if d.instance.Type() != instance.TypeContainer {
		return ErrUnsupportedDevType
	}

	rules := map[string]func(string) error{
		"vendorid":  shared.IsDeviceID,
		"productid": shared.IsDeviceID,
		"id":        shared.IsAny,
		"pci":       shared.IsAny,
		"uid":       unixValidUserID,
		"gid":       unixValidUserID,
		"mode":      unixValidOctalFileMode,
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	if d.config["pci"] != "" && (d.config["id"] != "" || d.config["productid"] != "" || d.config["vendorid"] != "") {
		return fmt.Errorf("Cannot use id, productid or vendorid when pci is set")
	}

	if d.config["id"] != "" && (d.config["pci"] != "" || d.config["productid"] != "" || d.config["vendorid"] != "") {
		return fmt.Errorf("Cannot use pci, productid or vendorid when id is set")
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
func (d *gpu) Start() (*RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	runConf := RunConfig{}
	gpus, err := resources.GetGPU()
	if err != nil {
		return nil, err
	}

	sawNvidia := false
	found := false
	for _, gpu := range gpus.Cards {
		if (d.config["vendorid"] != "" && gpu.VendorID != d.config["vendorid"]) ||
			(d.config["pci"] != "" && gpu.PCIAddress != d.config["pci"]) ||
			(d.config["productid"] != "" && gpu.ProductID != d.config["productid"]) {
			continue
		}

		// Handle DRM devices if present and matches criteria.
		if gpu.DRM != nil && (d.config["id"] == "" || fmt.Sprintf("%d", gpu.DRM.ID) == d.config["id"]) {
			found = true

			if gpu.DRM.CardName != "" && gpu.DRM.CardDevice != "" && shared.PathExists(filepath.Join(gpuDRIDevPath, gpu.DRM.CardName)) {
				path := filepath.Join(gpuDRIDevPath, gpu.DRM.CardName)
				major, minor, err := d.deviceNumStringToUint32(gpu.DRM.CardDevice)
				if err != nil {
					return nil, err
				}

				err = unixDeviceSetupCharNum(d.state, d.instance.DevicesPath(), "unix", d.name, d.config, major, minor, path, false, &runConf)
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

				err = unixDeviceSetupCharNum(d.state, d.instance.DevicesPath(), "unix", d.name, d.config, major, minor, path, false, &runConf)
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

				err = unixDeviceSetupCharNum(d.state, d.instance.DevicesPath(), "unix", d.name, d.config, major, minor, path, false, &runConf)
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

				err = unixDeviceSetupCharNum(d.state, d.instance.DevicesPath(), "unix", d.name, d.config, major, minor, path, false, &runConf)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	if sawNvidia {
		// No need to mount additional nvidia non-card devices as the nvidia.runtime
		// setting will do this for us.
		instanceConfig := d.instance.ExpandedConfig()
		if !shared.IsTrue(instanceConfig["nvidia.runtime"]) {
			nvidiaDevices, err := d.getNvidiaNonCardDevices()
			if err != nil {
				return nil, err
			}

			for _, dev := range nvidiaDevices {
				prefix := unixDeviceJoinPath("unix", d.name)
				if UnixDeviceExists(d.instance.DevicesPath(), prefix, dev.path) {
					continue
				}

				err = unixDeviceSetupCharNum(d.state, d.instance.DevicesPath(), "unix", d.name, d.config, dev.major, dev.minor, dev.path, false, &runConf)
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

// Stop is run when the device is removed from the instance.
func (d *gpu) Stop() (*RunConfig, error) {
	runConf := RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	err := unixDeviceRemove(d.instance.DevicesPath(), "unix", d.name, "", &runConf)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *gpu) postStop() error {
	// Remove host files for this device.
	err := unixDeviceDeleteFiles(d.state, d.instance.DevicesPath(), "unix", d.name, "")
	if err != nil {
		return fmt.Errorf("Failed to delete files for device '%s': %v", d.name, err)
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
			major: unix.Major(stat.Rdev),
			minor: unix.Minor(stat.Rdev),
		}

		nvidiaDevices = append(nvidiaDevices, tmpNividiaGpu)
	}

	return nvidiaDevices, nil
}
