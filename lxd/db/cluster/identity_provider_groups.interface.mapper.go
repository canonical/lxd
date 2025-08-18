//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// IdentityProviderGroupGenerated is an interface of generated methods for IdentityProviderGroup.
type IdentityProviderGroupGenerated interface {
	// GetIdentityProviderGroups returns all available identity_provider_groups.
	// generator: identity_provider_group GetMany
	GetIdentityProviderGroups(ctx context.Context, tx *sql.Tx, filters ...IdentityProviderGroupFilter) ([]IdentityProviderGroup, error)

	// GetIdentityProviderGroup returns the identity_provider_group with the given key.
	// generator: identity_provider_group GetOne
	GetIdentityProviderGroup(ctx context.Context, tx *sql.Tx, name string) (*IdentityProviderGroup, error)

	// CreateIdentityProviderGroup adds a new identity_provider_group to the database.
	// generator: identity_provider_group Create
	CreateIdentityProviderGroup(ctx context.Context, tx *sql.Tx, object IdentityProviderGroup) (int64, error)

	// DeleteIdentityProviderGroup deletes the identity_provider_group matching the given key parameters.
	// generator: identity_provider_group DeleteOne-by-Name
	DeleteIdentityProviderGroup(ctx context.Context, tx *sql.Tx, name string) error

	// RenameIdentityProviderGroup renames the identity_provider_group matching the given key parameters.
	// generator: identity_provider_group Rename
	RenameIdentityProviderGroup(ctx context.Context, tx *sql.Tx, name string, to string) error
}
