package api

import (
	"time"
)

// InstanceSnapshotsPost represents the fields available for a new LXD instance snapshot.
//
// swagger:model
//
// API extension: instances.
type InstanceSnapshotsPost struct {
	// Snapshot name
	// Example: snap0
	Name string `json:"name" yaml:"name"`

	// Whether the snapshot should include runtime state
	// Example: false
	Stateful bool `json:"stateful" yaml:"stateful"`

	// When the snapshot expires (gets auto-deleted)
	// Example: 2021-03-23T17:38:37.753398689-04:00
	//
	// API extension: snapshot_expiry_creation
	ExpiresAt *time.Time `json:"expires_at" yaml:"expires_at"`
}

// InstanceSnapshotPost represents the fields required to rename/move a LXD instance snapshot.
//
// swagger:model
//
// API extension: instances.
type InstanceSnapshotPost struct {
	// New name for the snapshot
	// Example: foo
	Name string `json:"name" yaml:"name"`

	// Whether this is a migration request
	// Example: false
	Migration bool `json:"migration" yaml:"migration"`

	// Migration target for push migration (requires migration)
	Target *InstancePostTarget `json:"target" yaml:"target"`

	// Whether to perform a live migration (requires migration)
	// Example: false
	Live bool `json:"live,omitempty" yaml:"live,omitempty"`
}

// InstanceSnapshotPut represents the modifiable fields of a LXD instance snapshot.
//
// swagger:model
//
// API extension: instances.
type InstanceSnapshotPut struct {
	// When the snapshot expires (gets auto-deleted)
	// Example: 2021-03-23T17:38:37.753398689-04:00
	ExpiresAt time.Time `json:"expires_at" yaml:"expires_at"`
}

// InstanceSnapshot represents a LXD instance snapshot.
//
// swagger:model
//
// API extension: instances.
type InstanceSnapshot struct {
	// Architecture name
	// Example: x86_64
	Architecture string `json:"architecture" yaml:"architecture"`

	// Instance configuration (see doc/instances.md)
	// Example: {"security.nesting": "true"}
	Config map[string]string `json:"config" yaml:"config"`

	// Instance creation timestamp
	// Example: 2021-03-23T20:00:00-04:00
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`

	// When the snapshot expires (gets auto-deleted)
	// Example: 2021-03-23T17:38:37.753398689-04:00
	ExpiresAt time.Time `json:"expires_at" yaml:"expires_at"`

	// Instance devices (see doc/instances.md)
	// Example: {"root": {"type": "disk", "pool": "default", "path": "/"}}
	Devices map[string]map[string]string `json:"devices" yaml:"devices"`

	// Whether the instance is ephemeral (deleted on shutdown)
	// Example: false
	Ephemeral bool `json:"ephemeral" yaml:"ephemeral"`

	// Expanded configuration (all profiles and local config merged)
	// Example: {"security.nesting": "true"}
	ExpandedConfig map[string]string `json:"expanded_config,omitempty" yaml:"expanded_config,omitempty"`

	// Expanded devices (all profiles and local devices merged)
	// Example: {"root": {"type": "disk", "pool": "default", "path": "/"}}
	ExpandedDevices map[string]map[string]string `json:"expanded_devices,omitempty" yaml:"expanded_devices,omitempty"`

	// Placement rules
	//
	// API extension: instance_placement_rules
	PlacementRules map[string]InstancePlacementRule `json:"placement_rules" yaml:"placement_rules"`

	// Expanded placement rules (all profiles and local placement rules merged)
	//
	// API extension: instance_placement_rules
	ExpandedPlacementRules map[string]InstancePlacementRule `json:"expanded_placement_rules" yaml:"expanded_placement_rules"`

	// Last start timestamp
	// Example: 2021-03-23T20:00:00-04:00
	LastUsedAt time.Time `json:"last_used_at" yaml:"last_used_at"`

	// Snapshot name
	// Example: foo
	Name string `json:"name" yaml:"name"`

	// List of profiles applied to the instance
	// Example: ["default"]
	Profiles []string `json:"profiles" yaml:"profiles"`

	// Whether the instance currently has saved state on disk
	// Example: false
	Stateful bool `json:"stateful" yaml:"stateful"`

	// Size of the snapshot in bytes
	// Example: 143360
	//
	// API extension: snapshot_disk_usage
	Size int64 `json:"size" yaml:"size"`
}

// Writable converts a full InstanceSnapshot struct into a InstanceSnapshotPut struct
// (filters read-only fields).
//
// API extension: instances.
func (c *InstanceSnapshot) Writable() InstanceSnapshotPut {
	return InstanceSnapshotPut{
		ExpiresAt: c.ExpiresAt,
	}
}

// SetWritable sets applicable values from InstanceSnapshotPut struct to InstanceSnapshot struct.
func (c *InstanceSnapshot) SetWritable(put InstanceSnapshotPut) {
	c.ExpiresAt = put.ExpiresAt
}
