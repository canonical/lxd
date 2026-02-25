//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// ImageRegistryGenerated is an interface of generated methods for ImageRegistry.
type ImageRegistryGenerated interface {
	// GetImageRegistrys returns all available image_registrys.
	// generator: image_registry GetMany
	GetImageRegistrys(ctx context.Context, tx *sql.Tx, filters ...ImageRegistryFilter) ([]ImageRegistry, error)

	// GetImageRegistry returns the image_registry with the given key.
	// generator: image_registry GetOne
	GetImageRegistry(ctx context.Context, tx *sql.Tx, name string) (*ImageRegistry, error)

	// GetImageRegistryID return the ID of the image_registry with the given key.
	// generator: image_registry ID
	GetImageRegistryID(ctx context.Context, tx *sql.Tx, name string) (int64, error)

	// ImageRegistryExists checks if a image_registry with the given key exists.
	// generator: image_registry Exists
	ImageRegistryExists(ctx context.Context, tx *sql.Tx, name string) (bool, error)

	// CreateImageRegistry adds a new image_registry to the database.
	// generator: image_registry Create
	CreateImageRegistry(ctx context.Context, tx *sql.Tx, object ImageRegistry) (int64, error)

	// UpdateImageRegistry updates the image_registry matching the given key parameters.
	// generator: image_registry Update
	UpdateImageRegistry(ctx context.Context, tx *sql.Tx, name string, object ImageRegistry) error

	// DeleteImageRegistry deletes the image_registry matching the given key parameters.
	// generator: image_registry DeleteOne-by-Name
	DeleteImageRegistry(ctx context.Context, tx *sql.Tx, name string) error

	// RenameImageRegistry renames the image_registry matching the given key parameters.
	// generator: image_registry Rename
	RenameImageRegistry(ctx context.Context, tx *sql.Tx, name string, to string) error
}
