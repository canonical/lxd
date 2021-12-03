//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// DeviceGenerated is an interface of generated methods for Device
type DeviceGenerated interface {
	// GetDevices returns all available devices for the parent entity.
	// generator: device GetMany
	GetDevices(parent string) (map[int][]Device, error)

	// CreateDevice adds a new device to the database.
	// generator: device Create
	CreateDevice(parent string, object Device) error

	// UpdateDevice updates the device matching the given key parameters.
	// generator: device Update
	UpdateDevice(parent string, referenceID int, devices map[string]Device) error

	// DeleteDevices deletes the device matching the given key parameters.
	// generator: device DeleteMany
	DeleteDevices(parent string, referenceID int) error
}
