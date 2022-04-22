//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// ProfileGenerated is an interface of generated methods for Profile
type ProfileGenerated interface {
	// GetProfileID return the ID of the profile with the given key.
	// generator: profile ID
	GetProfileID(ctx context.Context, tx *sql.Tx, project string, name string) (int64, error)
}
