//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// WarningGenerated is an interface of generated methods for Warning
type WarningGenerated interface {
	// GetWarnings returns all available warnings.
	// generator: warning GetMany
	GetWarnings(filter WarningFilter) ([]Warning, error)

	// GetWarning returns the warning with the given key.
	// generator: warning GetOne-by-UUID
	GetWarning(uuid string) (*Warning, error)

	// DeleteWarning deletes the warning matching the given key parameters.
	// generator: warning DeleteOne-by-UUID
	DeleteWarning(uuid string) error

	// DeleteWarnings deletes the warning matching the given key parameters.
	// generator: warning DeleteMany-by-EntityTypeCode-and-EntityID
	DeleteWarnings(entityTypeCode int, entityID int) error

	// GetWarningID return the ID of the warning with the given key.
	// generator: warning ID
	GetWarningID(uuid string) (int64, error)

	// WarningExists checks if a warning with the given key exists.
	// generator: warning Exists
	WarningExists(uuid string) (bool, error)
}
