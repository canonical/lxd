package device

import (
	"fmt"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/state"
)

// devTypes defines supported top-level device type creation functions.
var devTypes = map[string]func(deviceConfig.Device) device{
	"nic":          nicLoadByType,
	"infiniband":   infinibandLoadByType,
	"proxy":        func(c deviceConfig.Device) device { return &proxy{} },
	"gpu":          func(c deviceConfig.Device) device { return &gpu{} },
	"usb":          func(c deviceConfig.Device) device { return &usb{} },
	"unix-char":    func(c deviceConfig.Device) device { return &unixCommon{} },
	"unix-block":   func(c deviceConfig.Device) device { return &unixCommon{} },
	"unix-hotplug": func(c deviceConfig.Device) device { return &unixHotplug{} },
	"disk":         func(c deviceConfig.Device) device { return &disk{} },
	"none":         func(c deviceConfig.Device) device { return &none{} },
}

// load instantiates a device and initialises its internal state. It does not validate the config supplied.
func load(inst instance.Instance, state *state.State, name string, conf deviceConfig.Device, volatileGet VolatileGetter, volatileSet VolatileSetter) (device, error) {
	if conf["type"] == "" {
		return nil, fmt.Errorf("Missing device type for device %q", name)
	}

	devFunc := devTypes[conf["type"]]

	// Check if top-level type is recognised, if it is known type it will return a function.
	if devFunc == nil {
		return nil, ErrUnsupportedDevType
	}

	// Run the device create function and check it succeeds.
	dev := devFunc(conf)
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
	dev, err := load(inst, state, name, conf, volatileGet, volatileSet)
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
	dev, err := load(nil, state, name, conf, nil, nil)
	if err != nil {
		return err
	}

	return dev.validateConfig(instConfig)
}
