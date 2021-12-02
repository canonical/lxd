//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// ProfileGenerated is an interface of generated methods for Profile
type ProfileGenerated interface {
	// GetProfileURIs returns all available profile URIs.
	// generator: profile URIs
	GetProfileURIs(filter ProfileFilter) ([]string, error)

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

	// CreateProfile adds a new profile to the database.
	// generator: profile Create
	CreateProfile(object Profile) (int64, error)

	// RenameProfile renames the profile matching the given key parameters.
	// generator: profile Rename
	RenameProfile(project string, name string, to string) error

	// DeleteProfile deletes the profile matching the given key parameters.
	// generator: profile DeleteOne-by-Project-and-Name
	DeleteProfile(project string, name string) error

	// UpdateProfile updates the profile matching the given key parameters.
	// generator: profile Update
	UpdateProfile(project string, name string, object Profile) error
}
