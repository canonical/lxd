//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// ConfigGenerated is an interface of generated methods for Config
type ConfigGenerated interface {
	// GetConfig returns all available config.
	// generator: config GetMany
	GetConfig(parent string) (map[int]map[string]string, error)

	// CreateConfig adds a new config to the database.
	// generator: config Create
	CreateConfig(parent string, object Config) error

	// UpdateConfig updates the config matching the given key parameters.
	// generator: config Update
	UpdateConfig(parent string, referenceID int, config map[string]string) error

	// DeleteConfig deletes the config matching the given key parameters.
	// generator: config DeleteMany
	DeleteConfig(parent string, referenceID int) error
}
