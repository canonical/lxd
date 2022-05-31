//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// InstanceProfileGenerated is an interface of generated methods for InstanceProfile
type InstanceProfileGenerated interface {
	// GetProfileInstances returns all available Instances for the Profile.
	// generator: instance_profile GetMany
	GetProfileInstances(ctx context.Context, tx *sql.Tx, profileID int) ([]Instance, error)
}
