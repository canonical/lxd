//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// IdentityProjectGenerated is an interface of generated methods for IdentityProject.
type IdentityProjectGenerated interface {
	// GetIdentityProjects returns all available Projects for the Identity.
	// generator: identity_project GetMany
	GetIdentityProjects(ctx context.Context, tx *sql.Tx, identityID int) ([]Project, error)

	// DeleteIdentityProjects deletes the identity_project matching the given key parameters.
	// generator: identity_project DeleteMany
	DeleteIdentityProjects(ctx context.Context, tx *sql.Tx, identityID int) error

	// CreateIdentityProjects adds a new identity_project to the database.
	// generator: identity_project Create
	CreateIdentityProjects(ctx context.Context, tx *sql.Tx, objects []IdentityProject) error

	// UpdateIdentityProjects updates the identity_project matching the given key parameters.
	// generator: identity_project Update
	UpdateIdentityProjects(ctx context.Context, tx *sql.Tx, identityID int, projectNames []string) error
}
