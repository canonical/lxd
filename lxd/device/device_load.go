package device

import (
	"fmt"

	deviceConfig "github.com/grant-he/lxd/lxd/device/config"
	"github.com/grant-he/lxd/lxd/device/nictype"
	"github.com/grant-he/lxd/lxd/instance"
	"github.com/grant-he/lxd/lxd/state"
)

// load instantiates a device and initialises its internal state. It does not validate the config supplied.
func load(inst instance.Instance, state *state.State, projectName string, name string, conf deviceConfig.Device, volatileGet VolatileGetter, volatileSet VolatileSetter) (device, error) {
	// Warning: When validating a profile, inst is expected to be provided as nil.

	if conf["type"] == "" {
		return nil, fmt.Errorf("Missing device type for device %q", name)
	}

	// NIC type is required to lookup network devices.
	nicType, err := nictype.NICType(state, projectName, conf)
	if err != nil {
		return nil, err
	}

	// Lookup device type implementation.
	var dev device
	switch conf["type"] {
	case "nic":
		switch nicType {
		case "physical":
			dev = &nicPhysical{}
		case "ipvlan":
			dev = &nicIPVLAN{}
		case "p2p":
			dev = &nicP2P{}
		case "bridged":
			dev = &nicBridged{}
		case "routed":
			dev = &nicRouted{}
		case "macvlan":
			dev = &nicMACVLAN{}
		case "sriov":
			dev = &nicSRIOV{}
		case "ovn":
			dev = &nicOVN{}
		}
	case "infiniband":
		switch nicType {
		case "physical":
			dev = &infinibandPhysical{}
		case "sriov":
			dev = &infinibandSRIOV{}
		}
	case "proxy":
		dev = &proxy{}
	case "gpu":
		dev = &gpu{}
	case "usb":
		dev = &usb{}
	case "unix-char", "unix-block":
		dev = &unixCommon{}
	case "unix-hotplug":
		dev = &unixHotplug{}
	case "disk":
		dev = &disk{}
	case "none":
		dev = &none{}
	case "tpm":
		dev = &tpm{}
	}

	// Check a valid device type has been found.
	if dev == nil {
		return nil, ErrUnsupportedDevType
	}

	// Setup the device's internal variables.
	dev.init(inst, state, name, conf, volatileGet, volatileSet)

	return dev, nil
}

// New instantiates a new device struct, validates the supplied config and sets it into the device.
// If the device type is valid, but the other config validation fails then an instantiated device
// is still returned with the validation error. If an unknown device is requested or the device is
// not compatible with the instance type then an ErrUnsupportedDevType error is returned.
func New(inst instance.Instance, state *state.State, name string, conf deviceConfig.Device, volatileGet VolatileGetter, volatileSet VolatileSetter) (Device, error) {
	dev, err := load(inst, state, inst.Project(), name, conf, volatileGet, volatileSet)
	if err != nil {
		return nil, err
	}

	err = dev.validateConfig(inst)

	// We still return the instantiated device here, as in some scenarios the caller
	// may still want to use the device (such as when stopping or removing) even if
	// the config validation has failed.
	return dev, err
}

// Validate checks a device's config is valid. This only requires an instance.ConfigReader rather than an full
// blown instance to allow profile devices to be validated too.
func Validate(instConfig instance.ConfigReader, state *state.State, name string, conf deviceConfig.Device) error {
	dev, err := load(nil, state, instConfig.Project(), name, conf, nil, nil)
	if err != nil {
		return err
	}

	return dev.validateConfig(instConfig)
}
