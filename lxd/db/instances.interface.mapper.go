//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// InstanceGenerated is an interface of generated methods for Instance
type InstanceGenerated interface {
	// GetInstances returns all available instances.
	// generator: instance GetMany
	GetInstances(filter InstanceFilter) ([]Instance, error)

	// GetInstance returns the instance with the given key.
	// generator: instance GetOne
	GetInstance(project string, name string) (*Instance, error)

	// GetInstanceID return the ID of the instance with the given key.
	// generator: instance ID
	GetInstanceID(project string, name string) (int64, error)

	// InstanceExists checks if a instance with the given key exists.
	// generator: instance Exists
	InstanceExists(project string, name string) (bool, error)

	// CreateInstance adds a new instance to the database.
	// generator: instance Create
	CreateInstance(object Instance) (int64, error)

	// RenameInstance renames the instance matching the given key parameters.
	// generator: instance Rename
	RenameInstance(project string, name string, to string) error

	// DeleteInstance deletes the instance matching the given key parameters.
	// generator: instance DeleteOne-by-Project-and-Name
	DeleteInstance(project string, name string) error

	// UpdateInstance updates the instance matching the given key parameters.
	// generator: instance Update
	UpdateInstance(project string, name string, object Instance) error
}
