//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// AuthGroupGenerated is an interface of generated methods for AuthGroup.
type AuthGroupGenerated interface {
	// GetAuthGroups returns all available auth_groups.
	// generator: auth_group GetMany
	GetAuthGroups(ctx context.Context, tx *sql.Tx, filters ...AuthGroupFilter) ([]AuthGroup, error)

	// GetAuthGroup returns the auth_group with the given key.
	// generator: auth_group GetOne
	GetAuthGroup(ctx context.Context, tx *sql.Tx, name string) (*AuthGroup, error)

	// GetAuthGroupID return the ID of the auth_group with the given key.
	// generator: auth_group ID
	GetAuthGroupID(ctx context.Context, tx *sql.Tx, name string) (int64, error)

	// AuthGroupExists checks if a auth_group with the given key exists.
	// generator: auth_group Exists
	AuthGroupExists(ctx context.Context, tx *sql.Tx, name string) (bool, error)

	// CreateAuthGroup adds a new auth_group to the database.
	// generator: auth_group Create
	CreateAuthGroup(ctx context.Context, tx *sql.Tx, object AuthGroup) (int64, error)

	// DeleteAuthGroup deletes the auth_group matching the given key parameters.
	// generator: auth_group DeleteOne-by-Name
	DeleteAuthGroup(ctx context.Context, tx *sql.Tx, name string) error

	// UpdateAuthGroup updates the auth_group matching the given key parameters.
	// generator: auth_group Update
	UpdateAuthGroup(ctx context.Context, tx *sql.Tx, name string, object AuthGroup) error

	// RenameAuthGroup renames the auth_group matching the given key parameters.
	// generator: auth_group Rename
	RenameAuthGroup(ctx context.Context, tx *sql.Tx, name string, to string) error
}
