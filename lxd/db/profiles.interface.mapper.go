//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// ProfileGenerated is an interface of generated methods for Profile
type ProfileGenerated interface {
	// GetProfileDevices returns all available Profile Devices
	// generator: profile GetMany
	GetProfileDevices(profileID int) (map[string]Device, error)

	// GetProfileConfig returns all available Profile Config
	// generator: profile GetMany
	GetProfileConfig(profileID int) (map[string]string, error)

	// GetProfiles returns all available profiles.
	// generator: profile GetMany
	GetProfiles(filter ProfileFilter) ([]Profile, error)

	// GetProfile returns the profile with the given key.
	// generator: profile GetOne
	GetProfile(project string, name string) (*Profile, error)

	// ProfileExists checks if a profile with the given key exists.
	// generator: profile Exists
	ProfileExists(project string, name string) (bool, error)

	// GetProfileID return the ID of the profile with the given key.
	// generator: profile ID
	GetProfileID(project string, name string) (int64, error)

	// CreateProfileDevice adds a new profile Device to the database.
	// generator: profile Create
	CreateProfileDevice(profileID int64, device Device) error

	// CreateProfileConfig adds a new profile Config to the database.
	// generator: profile Create
	CreateProfileConfig(profileID int64, config map[string]string) error

	// CreateProfile adds a new profile to the database.
	// generator: profile Create
	CreateProfile(object Profile) (int64, error)

	// RenameProfile renames the profile matching the given key parameters.
	// generator: profile Rename
	RenameProfile(project string, name string, to string) error

	// DeleteProfile deletes the profile matching the given key parameters.
	// generator: profile DeleteOne-by-Project-and-Name
	DeleteProfile(project string, name string) error

	// UpdateProfileDevices updates the profile Device matching the given key parameters.
	// generator: profile Update
	UpdateProfileDevices(profileID int64, devices map[string]Device) error

	// UpdateProfileConfig updates the profile Config matching the given key parameters.
	// generator: profile Update
	UpdateProfileConfig(profileID int64, config map[string]string) error

	// UpdateProfile updates the profile matching the given key parameters.
	// generator: profile Update
	UpdateProfile(project string, name string, object Profile) error
}
