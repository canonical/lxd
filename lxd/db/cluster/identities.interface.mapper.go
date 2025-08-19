//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// IdentityGenerated is an interface of generated methods for Identity.
type IdentityGenerated interface {
	// GetIdentitys returns all available identitys.
	// generator: identity GetMany
	GetIdentitys(ctx context.Context, tx *sql.Tx, filters ...IdentityFilter) ([]Identity, error)

	// GetIdentity returns the identity with the given key.
	// generator: identity GetOne
	GetIdentity(ctx context.Context, tx *sql.Tx, authMethod AuthMethod, identifier string) (*Identity, error)

	// GetIdentityID return the ID of the identity with the given key.
	// generator: identity ID
	GetIdentityID(ctx context.Context, tx *sql.Tx, authMethod AuthMethod, identifier string) (int64, error)

	// CreateIdentity adds a new identity to the database.
	// generator: identity Create
	CreateIdentity(ctx context.Context, tx *sql.Tx, object Identity) (int64, error)

	// DeleteIdentity deletes the identity matching the given key parameters.
	// generator: identity DeleteOne-by-AuthMethod-and-Identifier
	DeleteIdentity(ctx context.Context, tx *sql.Tx, authMethod AuthMethod, identifier string) error

	// DeleteIdentitys deletes the identity matching the given key parameters.
	// generator: identity DeleteMany-by-Name-and-Type
	DeleteIdentitys(ctx context.Context, tx *sql.Tx, name string, identityType IdentityType) error

	// UpdateIdentity updates the identity matching the given key parameters.
	// generator: identity Update
	UpdateIdentity(ctx context.Context, tx *sql.Tx, authMethod AuthMethod, identifier string, object Identity) error
}
