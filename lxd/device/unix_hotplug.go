//go:build linux && cgo

package device

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jochenvg/go-udev"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/validate"
)

// unixHotplugDeviceMatch matches a unix-hotplug device based on vendorid, productid and/or subsystem. USB bus and devices with a major number of 0 are ignored. This function is used to indicate whether a unix hotplug event qualifies as part of our registered devices, and to load matching devices.
func unixHotplugDeviceMatch(config deviceConfig.Device, vendorid string, productid string, subsystem string, major uint32) bool {
	// Ignore devices with a major number of 0, since this indicates they are unnamed devices (e.g. non-device mounts).
	// Ignore USB bus devices (handled by `usb` device type) since we don't want `unix-hotplug` and `usb` devices conflicting.
	// We want to add all device nodes besides those with a `usb` subsystem.
	if major == 0 || strings.HasPrefix(subsystem, "usb") {
		return false
	}

	if config["vendorid"] != "" && config["vendorid"] != vendorid {
		return false
	}

	if config["productid"] != "" && config["productid"] != productid {
		return false
	}

	if config["subsystem"] != "" && config["subsystem"] != subsystem {
		return false
	}

	return true
}

type unixHotplug struct {
	deviceCommon
}

// isRequired indicates whether the device config requires this device to start OK.
func (d *unixHotplug) isRequired() bool {
	// Defaults to not required.
	return shared.IsTrue(d.config["required"])
}

// validateConfig checks the supplied config for correctness.
func (d *unixHotplug) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container) {
		return ErrUnsupportedDevType
	}

	rules := map[string]func(string) error{
		// lxdmeta:generate(entities=device-unix-hotplug; group=device-conf; key=vendorid)
		//
		// ---
		//  type: string
		//  shortdesc: Vendor ID of the Unix device

		// lxdmeta:generate(entities=device-unix-usb; group=device-conf; key=vendorid)
		//
		// ---
		//  type: string
		//  shortdesc: Vendor ID of the USB device
		"vendorid": validate.Optional(validate.IsDeviceID),
		// lxdmeta:generate(entities=device-unix-hotplug; group=device-conf; key=productid)
		//
		// ---
		//  type: string
		//  shortdesc: Product ID of the Unix device

		// lxdmeta:generate(entities=device-unix-usb; group=device-conf; key=productid)
		//
		// ---
		//  type: string
		//  shortdesc: Product ID of the USB device
		"productid": validate.Optional(validate.IsDeviceID),

		// lxdmeta:generate(entities=device-unix-hotplug; group=device-conf; key=subsystem)
		//
		// ---
		// type: string
		// shortdesc: Subsystem of the Unix device
		"subsystem": validate.IsAny,
		"uid":       unixValidUserID,
		"gid":       unixValidUserID,
		"mode":      unixValidOctalFileMode,

		// lxdmeta:generate(entities=device-unix-hotplug; group=device-conf; key=required)
		// The default is `false`, which means that all devices can be hotplugged.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  shortdesc: Whether this device is required to start the container
		"required": validate.Optional(validate.IsBool),

		// lxdmeta:generate(entities=device-unix-hotplug; group=device-conf; key=ownership.inherit)
		//
		// ---
		// type: bool
		// defaultdesc: `false`
		// shortdesc: Whether this device inherits ownership (GID and/or UID) from the host
		"ownership.inherit": validate.Optional(validate.IsBool),
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	if d.config["vendorid"] == "" && d.config["productid"] == "" && d.config["subsystem"] == "" {
		return errors.New("Unix hotplug devices require a vendorid, productid or subsystem")
	}

	if d.config["gid"] != "" && d.config["uid"] != "" && shared.IsTrue(d.config["ownership.inherit"]) {
		return errors.New("Unix hotplug device ownership cannot be inherited from host while GID and UID are set")
	}

	return nil
}

// Register is run after the device is started or when LXD starts.
func (d *unixHotplug) Register() error {
	// Extract variables needed to run the event hook so that the reference to this device struct is not required to be stored in memory.
	devicesPath := d.inst.DevicesPath()
	devConfig := d.config
	deviceName := d.name
	state := d.state

	// Handler for when a UnixHotplug event occurs.
	f := func(e UnixHotplugEvent) (*deviceConfig.RunConfig, error) {
		runConf := deviceConfig.RunConfig{}

		switch e.Action {
		case "add":
			if !unixHotplugDeviceMatch(devConfig, e.Vendor, e.Product, e.Subsystem, e.Major) {
				return nil, nil
			}

			if e.Subsystem == "block" {
				err := unixDeviceSetupBlockNum(state, devicesPath, "unix", deviceName, devConfig, e.Major, e.Minor, e.Path, false, &runConf)
				if err != nil {
					return nil, err
				}
			} else {
				err := unixDeviceSetupCharNum(state, devicesPath, "unix", deviceName, devConfig, e.Major, e.Minor, e.Path, false, &runConf)
				if err != nil {
					return nil, err
				}
			}
		case "remove":
			relativeTargetPath := strings.TrimPrefix(e.Path, "/")
			err := unixDeviceRemove(devicesPath, "unix", deviceName, relativeTargetPath, &runConf)
			if err != nil {
				return nil, err
			}

			// Add a post hook function to remove the specific unix hotplug device file after unmount.
			runConf.PostHooks = []func() error{func() error {
				err := unixDeviceDeleteFiles(state, devicesPath, "unix", deviceName, relativeTargetPath)
				if err != nil {
					return fmt.Errorf("Failed to delete files for device %q: %w", deviceName, err)
				}

				return nil
			}}
		}

		runConf.Uevents = append(runConf.Uevents, e.UeventParts)

		return &runConf, nil
	}

	unixHotplugRegisterHandler(d.inst, d.name, f)

	return nil
}

// Start is run when the device is added to the instance.
func (d *unixHotplug) Start() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{}
	runConf.PostHooks = []func() error{d.Register}

	devices := d.loadUnixDevices()
	if d.isRequired() && len(devices) <= 0 {
		return nil, errors.New("Required unix hotplug device not found")
	}

	for _, device := range devices {
		devnum := device.Devnum()
		major := uint32(devnum.Major())
		minor := uint32(devnum.Minor())

		// Setup device.
		var err error
		if device.Subsystem() == "block" {
			err = unixDeviceSetupBlockNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, major, minor, device.Devnode(), false, &runConf)

			if err != nil {
				return nil, err
			}
		} else {
			err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, major, minor, device.Devnode(), false, &runConf)

			if err != nil {
				return nil, err
			}
		}

		// Remove unix device on failure to setup device.
		runConf.Revert = func() { _ = unixDeviceRemove(d.inst.DevicesPath(), "unix", d.name, "", &runConf) }

		if err != nil {
			return nil, fmt.Errorf("Unable to setup unix hotplug device: %w", err)
		}
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *unixHotplug) Stop() (*deviceConfig.RunConfig, error) {
	unixHotplugUnregisterHandler(d.inst, d.name)

	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	err := unixDeviceRemove(d.inst.DevicesPath(), "unix", d.name, "", &runConf)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *unixHotplug) postStop() error {
	err := unixDeviceDeleteFiles(d.state, d.inst.DevicesPath(), "unix", d.name, "")
	if err != nil {
		return fmt.Errorf("Failed to delete files for device %q: %w", d.name, err)
	}

	return nil
}

// loadUnixDevices scans the host machine for unix devices with matching product/vendor ids and returns the matching devices with subsystem types of char or block.
func (d *unixHotplug) loadUnixDevices() []udev.Device {
	// Find device if exists
	u := udev.Udev{}
	e := u.NewEnumerate()

	if d.config["vendorid"] != "" {
		err := e.AddMatchProperty("ID_VENDOR_ID", d.config["vendorid"])
		if err != nil {
			logger.Warn("Failed to add property to device", logger.Ctx{"property_name": "ID_VENDOR_ID", "property_value": d.config["vendorid"], "err": err})
		}
	}

	if d.config["productid"] != "" {
		err := e.AddMatchProperty("ID_MODEL_ID", d.config["productid"])
		if err != nil {
			logger.Warn("Failed to add property to device", logger.Ctx{"property_name": "ID_MODEL_ID", "property_value": d.config["productid"], "err": err})
		}
	}

	if d.config["subsystem"] != "" {
		err := e.AddMatchProperty("SUBSYSTEM", d.config["subsystem"])
		if err != nil {
			logger.Warn("Failed to add property to device", logger.Ctx{"property_name": "SUBSYSTEM", "property_value": d.config["subsystem"]})
		}
	}

	err := e.AddMatchIsInitialized()
	if err != nil {
		logger.Warn("Failed to add initialised property to device", logger.Ctx{"err": err})
	}

	devices, _ := e.Devices()
	var matchingDevices []udev.Device //nolint:prealloc
	for i := range devices {
		device := devices[i]

		// We ignore devices without an associated device node file name, as this indicates they are not accessible via the standard interface in /dev/.
		if device == nil || device.Devnode() == "" {
			continue
		}

		match := unixHotplugDeviceMatch(d.config, device.PropertyValue("ID_VENDOR_ID"), device.PropertyValue("ID_MODEL_ID"), device.Subsystem(), uint32(device.Devnum().Major()))

		if !match {
			continue
		}

		matchingDevices = append(matchingDevices, *device)
	}

	return matchingDevices
}
