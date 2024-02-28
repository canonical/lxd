//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"

	"github.com/canonical/lxd/lxd/auth"
)

// PermissionGenerated is an interface of generated methods for Permission.
type PermissionGenerated interface {
	// GetPermissions returns all available permissions.
	// generator: permission GetMany
	GetPermissions(ctx context.Context, tx *sql.Tx, filters ...PermissionFilter) ([]Permission, error)

	// GetPermission returns the permission with the given key.
	// generator: permission GetOne
	GetPermission(ctx context.Context, tx *sql.Tx, entitlement auth.Entitlement, entityType EntityType, entityID int) (*Permission, error)
}
