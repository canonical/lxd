//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// InstanceProfileGenerated is an interface of generated methods for InstanceProfile
type InstanceProfileGenerated interface {
	// GetProfileInstances returns all available instance_profiles.
	// generator: instance_profile GetMany
	GetProfileInstances() (map[int][]int, error)

	// GetInstanceProfiles returns all available instance_profiles.
	// generator: instance_profile GetMany
	GetInstanceProfiles() (map[int][]int, error)

	// CreateInstanceProfile adds a new instance_profile to the database.
	// generator: instance_profile Create
	CreateInstanceProfile(object InstanceProfile) (int64, error)

	// DeleteInstanceProfiles deletes the instance_profile matching the given key parameters.
	// generator: instance_profile DeleteMany
	DeleteInstanceProfiles(object Instance) error
}
