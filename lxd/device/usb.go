package device

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/validate"
)

// usbDevPath is the path where USB devices can be enumerated.
const usbDevPath = "/sys/bus/usb/devices"

// usbIsOurDevice indicates whether the USB device event qualifies as part of our device.
// This function is not defined against the usb struct type so that it can be used in event
// callbacks without needing to keep a reference to the usb device struct.
func usbIsOurDevice(config deviceConfig.Device, usb *USBEvent) bool {
	// Check if event matches criteria for this device, if not return.
	if (config["vendorid"] != "" && config["vendorid"] != usb.Vendor) || (config["productid"] != "" && config["productid"] != usb.Product) {
		return false
	}

	return true
}

type usb struct {
	deviceCommon
}

// isRequired indicates whether the device config requires this device to start OK.
func (d *usb) isRequired() bool {
	// Defaults to not required.
	if shared.IsTrue(d.config["required"]) {
		return true
	}

	return false
}

// validateConfig checks the supplied config for correctness.
func (d *usb) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	if instConf.Architecture() == osarch.ARCH_64BIT_S390_BIG_ENDIAN {
		return fmt.Errorf("USB devices aren't supported on s390x")
	}

	rules := map[string]func(string) error{
		"vendorid":  validate.Optional(validate.IsDeviceID),
		"productid": validate.Optional(validate.IsDeviceID),
		"uid":       unixValidUserID,
		"gid":       unixValidUserID,
		"mode":      unixValidOctalFileMode,
		"required":  validate.Optional(validate.IsBool),
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	return nil
}

// Register is run after the device is started or when LXD starts.
func (d *usb) Register() error {
	// Extract variables needed to run the event hook so that the reference to this device
	// struct is not needed to be kept in memory.
	devicesPath := d.inst.DevicesPath()
	devConfig := d.config
	deviceName := d.name
	state := d.state

	// Handler for when a USB event occurs.
	f := func(e USBEvent) (*deviceConfig.RunConfig, error) {
		if !usbIsOurDevice(devConfig, &e) {
			return nil, nil
		}

		runConf := deviceConfig.RunConfig{}

		if e.Action == "add" {
			err := unixDeviceSetupCharNum(state, devicesPath, "unix", deviceName, devConfig, e.Major, e.Minor, e.Path, false, &runConf)
			if err != nil {
				return nil, err
			}
		} else if e.Action == "remove" {
			relativeTargetPath := strings.TrimPrefix(e.Path, "/")
			err := unixDeviceRemove(devicesPath, "unix", deviceName, relativeTargetPath, &runConf)
			if err != nil {
				return nil, err
			}

			// Add a post hook function to remove the specific USB device file after unmount.
			runConf.PostHooks = []func() error{func() error {
				err := unixDeviceDeleteFiles(state, devicesPath, "unix", deviceName, relativeTargetPath)
				if err != nil {
					return fmt.Errorf("Failed to delete files for device '%s': %v", deviceName, err)
				}

				return nil
			}}
		}

		runConf.Uevents = append(runConf.Uevents, e.UeventParts)

		return &runConf, nil
	}

	usbRegisterHandler(d.inst, d.name, f)

	return nil
}

// Start is run when the device is added to the instance.
func (d *usb) Start() (*deviceConfig.RunConfig, error) {
	if d.inst.Type() == instancetype.VM {
		return d.startVM()
	}

	return d.startContainer()
}

func (d *usb) startContainer() (*deviceConfig.RunConfig, error) {
	usbs, err := d.loadUsb()
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	runConf.PostHooks = []func() error{d.Register}

	for _, usb := range usbs {
		if !usbIsOurDevice(d.config, &usb) {
			continue
		}

		err := unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, usb.Major, usb.Minor, usb.Path, false, &runConf)
		if err != nil {
			return nil, err
		}
	}

	if d.isRequired() && len(runConf.Mounts) <= 0 {
		return nil, fmt.Errorf("Required USB device not found")
	}

	return &runConf, nil
}

func (d *usb) startVM() (*deviceConfig.RunConfig, error) {
	usbs, err := d.loadUsb()
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	runConf.PostHooks = []func() error{d.Register}

	for _, usb := range usbs {
		if !usbIsOurDevice(d.config, &usb) {
			continue
		}

		runConf.USBDevice = append(runConf.USBDevice, []deviceConfig.RunConfigItem{
			{Key: "devName", Value: d.name},
			{Key: "hostDevice", Value: fmt.Sprintf("/dev/bus/usb/%03d/%03d", usb.BusNum, usb.DevNum)},
		}...)
	}

	if d.isRequired() && len(runConf.USBDevice) <= 0 {
		return nil, fmt.Errorf("Required USB device not found")
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *usb) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	if d.inst.Type() == instancetype.Container {
		// Unregister any USB event handlers for this device.
		usbUnregisterHandler(d.inst, d.name)

		err := unixDeviceRemove(d.inst.DevicesPath(), "unix", d.name, "", &runConf)
		if err != nil {
			return nil, err
		}
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *usb) postStop() error {
	// Remove host files for this device.
	err := unixDeviceDeleteFiles(d.state, d.inst.DevicesPath(), "unix", d.name, "")
	if err != nil {
		return fmt.Errorf("Failed to delete files for device '%s': %v", d.name, err)
	}

	return nil
}

// loadUsb scans the host machine for USB devices.
func (d *usb) loadUsb() ([]USBEvent, error) {
	result := []USBEvent{}

	ents, err := ioutil.ReadDir(usbDevPath)
	if err != nil {
		/* if there are no USB devices, let's render an empty list,
		 * i.e. no usb devices */
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}

	for _, ent := range ents {
		values, err := d.loadRawValues(path.Join(usbDevPath, ent.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return []USBEvent{}, err
		}

		parts := strings.Split(values["dev"], ":")
		if len(parts) != 2 {
			return []USBEvent{}, fmt.Errorf("invalid device value %s", values["dev"])
		}

		usb, err := USBNewEvent(
			"add",
			values["idVendor"],
			values["idProduct"],
			parts[0],
			parts[1],
			values["busnum"],
			values["devnum"],
			values["devname"],
			[]string{},
			0,
		)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		result = append(result, usb)
	}

	return result, nil
}

func (d *usb) loadRawValues(p string) (map[string]string, error) {
	values := map[string]string{
		"idVendor":  "",
		"idProduct": "",
		"dev":       "",
		"busnum":    "",
		"devnum":    "",
	}

	for k := range values {
		v, err := ioutil.ReadFile(path.Join(p, k))
		if err != nil {
			return nil, err
		}

		values[k] = strings.TrimSpace(string(v))
	}

	return values, nil
}
