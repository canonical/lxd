//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// InstanceSnapshotGenerated is an interface of generated methods for InstanceSnapshot
type InstanceSnapshotGenerated interface {
	// GetInstanceSnapshots returns all available instance_snapshots.
	// generator: instance_snapshot List
	GetInstanceSnapshots(filter InstanceSnapshotFilter) ([]InstanceSnapshot, error)

	// GetInstanceSnapshot returns the instance_snapshot with the given key.
	// generator: instance_snapshot Get
	GetInstanceSnapshot(project string, instance string, name string) (*InstanceSnapshot, error)

	// GetInstanceSnapshotID return the ID of the instance_snapshot with the given key.
	// generator: instance_snapshot ID
	GetInstanceSnapshotID(project string, instance string, name string) (int64, error)

	// InstanceSnapshotExists checks if a instance_snapshot with the given key exists.
	// generator: instance_snapshot Exists
	InstanceSnapshotExists(project string, instance string, name string) (bool, error)

	// CreateInstanceSnapshot adds a new instance_snapshot to the database.
	// generator: instance_snapshot Create
	CreateInstanceSnapshot(object InstanceSnapshot) (int64, error)

	// InstanceSnapshotConfigRef returns entities used by instance_snapshots.
	// generator: instance_snapshot ConfigRef
	InstanceSnapshotConfigRef(filter InstanceSnapshotFilter) (map[string]map[string]map[string]map[string]string, error)

	// InstanceSnapshotDevicesRef returns entities used by instance_snapshots.
	// generator: instance_snapshot DevicesRef
	InstanceSnapshotDevicesRef(filter InstanceSnapshotFilter) (map[string]map[string]map[string]map[string]map[string]string, error)

	// RenameInstanceSnapshot renames the instance_snapshot matching the given key parameters.
	// generator: instance_snapshot Rename
	RenameInstanceSnapshot(project string, instance string, name string, to string) error

	// DeleteInstanceSnapshot deletes the instance_snapshot matching the given key parameters.
	// generator: instance_snapshot DeleteOne-by-Project-and-Instance-and-Name
	DeleteInstanceSnapshot(project string, instance string, name string) error
}
