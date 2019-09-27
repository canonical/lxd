package device

import (
	"fmt"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/state"
)

// devTypes defines supported top-level device type creation functions.
var devTypes = map[string]func(deviceConfig.Device) device{
	"nic":        nicLoadByType,
	"infiniband": infinibandLoadByType,
	"proxy":      func(c deviceConfig.Device) device { return &proxy{} },
	"gpu":        func(c deviceConfig.Device) device { return &gpu{} },
	"usb":        func(c deviceConfig.Device) device { return &usb{} },
	"unix-char":  func(c deviceConfig.Device) device { return &unixCommon{} },
	"unix-block": func(c deviceConfig.Device) device { return &unixCommon{} },
	"disk":       func(c deviceConfig.Device) device { return &disk{} },
	"none":       func(c deviceConfig.Device) device { return &none{} },
}

// VolatileSetter is a function that accepts one or more key/value strings to save into the LXD
// config for this instance. It should add the volatile device name prefix to each key when saving.
type VolatileSetter func(map[string]string) error

// VolatileGetter is a function that retrieves any key/value string that exists in the LXD database
// config for this instance. It should only return keys that match the volatile device name prefix,
// and should remove the prefix before being returned.
type VolatileGetter func() map[string]string

// Device represents a device that can be added to an instance.
type Device interface {
	// CanHotPlug returns true if device can be managed whilst instance is running.
	// It also returns a slice of config fields that can be live updated. If only fields in this
	// list have changed then Update() is called rather than triggering a device remove & add.
	CanHotPlug() (bool, []string)

	// Add performs any host-side setup when a device is added to an instance.
	// It is called irrespective of whether the instance is running or not.
	Add() error

	// Start peforms any host-side configuration required to start the device for the instance.
	// This can be when a device is plugged into a running instance or the instance is starting.
	// Returns run-time configuration needed for configuring the instance with the new device.
	Start() (*RunConfig, error)

	// Register provides the ability for a device to subcribe to events that LXD can generate.
	// It is called after a device is started (after Start()) or when LXD starts.
	Register() error

	// Update performs host-side modifications for a device based on the difference between the
	// current config and previous devices config supplied as an argument. This called if the
	// only config fields that have changed are supplied in the list returned from CanHotPlug().
	// The function also accepts a boolean indicating whether the instance is running or not.
	Update(oldDevices deviceConfig.Devices, running bool) error

	// Stop performs any host-side cleanup required when a device is removed from an instance,
	// either due to unplugging it from a running instance or instance is being shutdown.
	// Returns run-time configuration needed for detaching the device from the instance.
	Stop() (*RunConfig, error)

	// Remove performs any host-side cleanup when an instance is removed from an instance.
	Remove() error
}

// device represents a sealed interface that implements Device, but also contains some internal
// setup functions for a Device that should only be called by device.New() to avoid exposing devices
// that are not in a known configured state. This is separate from the Device interface so that
// Devices created outside of the device package can be used by LXD, but ensures that any devices
// created by the device package will only be accessible after being configured properly by New().
type device interface {
	Device

	// init stores the Instance, daemon State and Config into device and performs any setup.
	init(Instance, *state.State, string, deviceConfig.Device, VolatileGetter, VolatileSetter)

	// validateConfig checks Config stored by init() is valid for the instance type.
	validateConfig() error
}

// deviceCommon represents the common struct for all devices.
type deviceCommon struct {
	instance    Instance
	name        string
	config      deviceConfig.Device
	state       *state.State
	volatileGet func() map[string]string
	volatileSet func(map[string]string) error
}

// init stores the Instance, daemon state, device name and config into device.
// It also needs to be provided with volatile get and set functions for the device to allow
// persistent data to be accessed. This is implemented as part of deviceCommon so that the majority
// of devices don't need to implement it and can just embed deviceCommon.
func (d *deviceCommon) init(instance Instance, state *state.State, name string, conf deviceConfig.Device, volatileGet VolatileGetter, volatileSet VolatileSetter) {
	d.instance = instance
	d.name = name
	d.config = conf
	d.state = state
	d.volatileGet = volatileGet
	d.volatileSet = volatileSet
}

// Add returns nil error as majority of devices don't need to do any host-side setup.
func (d *deviceCommon) Add() error {
	return nil
}

// Register returns nil error as majority of devices don't need to do any event registration.
func (d *deviceCommon) Register() error {
	return nil
}

// CanHotPlug returns true as majority of devices can be started/stopped when instance is running.
// Also returns an empty list of update fields as most devices do not support live updates.
func (d *deviceCommon) CanHotPlug() (bool, []string) {
	return true, []string{}
}

// Update returns an error as most devices do not support live updates without being restarted.
func (d *deviceCommon) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	return fmt.Errorf("Device does not support updates whilst started")
}

// Remove returns nil error as majority of devices don't need to do any host-side cleanup on delete.
func (d *deviceCommon) Remove() error {
	return nil
}

// New instantiates a new device struct, validates the supplied config and sets it into the device.
// If the device type is valid, but the other config validation fails then an instantiated device
// is still returned with the validation error. If an unknown device is requested or the device is
// not compatible with the instance type then an ErrUnsupportedDevType error is returned.
func New(instance Instance, state *state.State, name string, conf deviceConfig.Device, volatileGet VolatileGetter, volatileSet VolatileSetter) (Device, error) {
	if conf["type"] == "" {
		return nil, fmt.Errorf("Missing device type for device '%s'", name)
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

	// Init the device and run validation of supplied config.
	dev.init(instance, state, name, conf, volatileGet, volatileSet)
	err := dev.validateConfig()

	// We still return the instantiated device here, as in some scenarios the caller
	// may still want to use the device (such as when stopping or removing) even if
	// the config validation has failed.
	return dev, err
}
