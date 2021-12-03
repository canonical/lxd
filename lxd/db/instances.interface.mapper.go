//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// InstanceGenerated is an interface of generated methods for Instance
type InstanceGenerated interface {
	// GetInstanceDevices returns all available Instance Devices
	// generator: instance GetMany
	GetInstanceDevices(instanceID int) (map[string]Device, error)

	// GetInstanceConfig returns all available Instance Config
	// generator: instance GetMany
	GetInstanceConfig(instanceID int) (map[string]string, error)

	// GetInstances returns all available instances.
	// generator: instance GetMany
	GetInstances(filter InstanceFilter) ([]Instance, error)

	// GetInstance returns the instance with the given key.
	// generator: instance GetOne
	GetInstance(project string, name string) (*Instance, error)

	// GetInstanceURIs returns all available instance URIs.
	// generator: instance URIs
	GetInstanceURIs(filter InstanceFilter) ([]string, error)

	// GetInstanceID return the ID of the instance with the given key.
	// generator: instance ID
	GetInstanceID(project string, name string) (int64, error)

	// InstanceExists checks if a instance with the given key exists.
	// generator: instance Exists
	InstanceExists(project string, name string) (bool, error)

	// CreateInstanceDevice adds a new instance Device to the database.
	// generator: instance Create
	CreateInstanceDevice(instanceID int64, device Device) error

	// CreateInstanceConfig adds a new instance Config to the database.
	// generator: instance Create
	CreateInstanceConfig(instanceID int64, config map[string]string) error

	// CreateInstance adds a new instance to the database.
	// generator: instance Create
	CreateInstance(object Instance) (int64, error)

	// RenameInstance renames the instance matching the given key parameters.
	// generator: instance Rename
	RenameInstance(project string, name string, to string) error

	// DeleteInstance deletes the instance matching the given key parameters.
	// generator: instance DeleteOne-by-Project-and-Name
	DeleteInstance(project string, name string) error

	// UpdateInstanceDevices updates the instance Device matching the given key parameters.
	// generator: instance Update
	UpdateInstanceDevices(instanceID int64, devices map[string]Device) error

	// UpdateInstanceConfig updates the instance Config matching the given key parameters.
	// generator: instance Update
	UpdateInstanceConfig(instanceID int64, config map[string]string) error

	// UpdateInstance updates the instance matching the given key parameters.
	// generator: instance Update
	UpdateInstance(project string, name string, object Instance) error
}
