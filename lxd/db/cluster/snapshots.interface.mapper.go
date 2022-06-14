//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// InstanceSnapshotGenerated is an interface of generated methods for InstanceSnapshot
type InstanceSnapshotGenerated interface {
	// GetInstanceSnapshotConfig returns all available InstanceSnapshot Config
	// generator: instance_snapshot GetMany
	GetInstanceSnapshotConfig(ctx context.Context, tx *sql.Tx, instanceSnapshotID int) (map[string]string, error)

	// GetInstanceSnapshotDevices returns all available InstanceSnapshot Devices
	// generator: instance_snapshot GetMany
	GetInstanceSnapshotDevices(ctx context.Context, tx *sql.Tx, instanceSnapshotID int) (map[string]Device, error)

	// GetInstanceSnapshots returns all available instance_snapshots.
	// generator: instance_snapshot GetMany
	GetInstanceSnapshots(ctx context.Context, tx *sql.Tx, filter InstanceSnapshotFilter) ([]InstanceSnapshot, error)
}
