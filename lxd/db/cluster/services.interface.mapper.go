//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// ServiceGenerated is an interface of generated methods for Service.
type ServiceGenerated interface {
	// GetServiceConfig returns all available Service Config
	// generator: service GetMany
	GetServiceConfig(ctx context.Context, tx *sql.Tx, serviceID int, filters ...ConfigFilter) (map[string]string, error)

	// GetServices returns all available services.
	// generator: service GetMany
	GetServices(ctx context.Context, tx *sql.Tx, filters ...ServiceFilter) ([]Service, error)

	// GetService returns the service with the given key.
	// generator: service GetOne
	GetService(ctx context.Context, tx *sql.Tx, name string) (*Service, error)

	// GetServiceID return the ID of the service with the given key.
	// generator: service ID
	GetServiceID(ctx context.Context, tx *sql.Tx, name string) (int64, error)

	// ServiceExists checks if a service with the given key exists.
	// generator: service Exists
	ServiceExists(ctx context.Context, tx *sql.Tx, name string) (bool, error)

	// CreateServiceConfig adds new service Config to the database.
	// generator: service Create
	CreateServiceConfig(ctx context.Context, tx *sql.Tx, serviceID int64, config map[string]string) error

	// CreateService adds a new service to the database.
	// generator: service Create
	CreateService(ctx context.Context, tx *sql.Tx, object Service) (int64, error)

	// DeleteService deletes the service matching the given key parameters.
	// generator: service DeleteOne-by-Name
	DeleteService(ctx context.Context, tx *sql.Tx, name string) error

	// UpdateServiceConfig updates the service Config matching the given key parameters.
	// generator: service Update
	UpdateServiceConfig(ctx context.Context, tx *sql.Tx, serviceID int64, config map[string]string) error

	// UpdateService updates the service matching the given key parameters.
	// generator: service Update
	UpdateService(ctx context.Context, tx *sql.Tx, name string, object Service) error

	// RenameService renames the service matching the given key parameters.
	// generator: service Rename
	RenameService(ctx context.Context, tx *sql.Tx, name string, to string) error
}
