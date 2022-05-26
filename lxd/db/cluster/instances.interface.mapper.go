//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// InstanceGenerated is an interface of generated methods for Instance
type InstanceGenerated interface {
	// GetInstances returns all available instances.
	// generator: instance GetMany
	GetInstances(ctx context.Context, tx *sql.Tx, filter InstanceFilter) ([]Instance, error)
}
