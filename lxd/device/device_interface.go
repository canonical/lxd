package device

import (
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/api"
)

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
	Start() (*deviceConfig.RunConfig, error)

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
	Stop() (*deviceConfig.RunConfig, error)

	// Remove performs any host-side cleanup when a device is removed from an instance.
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
	init(instance.Instance, *state.State, string, deviceConfig.Device, VolatileGetter, VolatileSetter)

	// validateConfig checks Config stored by init() is valid for the instance type.
	validateConfig(instance.ConfigReader) error
}

// NICState provides the ability to access NIC state.
type NICState interface {
	State() (*api.InstanceStateNetwork, error)
}
