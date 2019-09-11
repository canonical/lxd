package api

import (
	"time"
)

// InstanceSnapshotsPost represents the fields available for a new LXD instance snapshot.
//
// API extension: instances
type InstanceSnapshotsPost struct {
	Name     string `json:"name" yaml:"name"`
	Stateful bool   `json:"stateful" yaml:"stateful"`

	// API extension: snapshot_expiry_creation
	ExpiresAt *time.Time `json:"expires_at" yaml:"expires_at"`
}

// ContainerSnapshotsPost represents the fields available for a new LXD container snapshot.
type ContainerSnapshotsPost InstanceSnapshotsPost

// InstanceSnapshotPost represents the fields required to rename/move a LXD instance snapshot.
//
// API extension: instances
type InstanceSnapshotPost struct {
	Name      string              `json:"name" yaml:"name"`
	Migration bool                `json:"migration" yaml:"migration"`
	Target    *InstancePostTarget `json:"target" yaml:"target"`

	// API extension: container_snapshot_stateful_migration
	Live bool `json:"live,omitempty" yaml:"live,omitempty"`
}

// ContainerSnapshotPost represents the fields required to rename/move a LXD container snapshot.
type ContainerSnapshotPost struct {
	Name      string               `json:"name" yaml:"name"`
	Migration bool                 `json:"migration" yaml:"migration"`
	Target    *ContainerPostTarget `json:"target" yaml:"target"`

	// API extension: container_snapshot_stateful_migration
	Live bool `json:"live,omitempty" yaml:"live,omitempty"`
}

// InstanceSnapshotPut represents the modifiable fields of a LXD instance snapshot.
//
// API extension: instances
type InstanceSnapshotPut struct {
	Architecture string                       `json:"architecture" yaml:"architecture"`
	Config       map[string]string            `json:"config" yaml:"config"`
	Devices      map[string]map[string]string `json:"devices" yaml:"devices"`
	Ephemeral    bool                         `json:"ephemeral" yaml:"ephemeral"`
	Profiles     []string                     `json:"profiles" yaml:"profiles"`
	ExpiresAt    time.Time                    `json:"expires_at" yaml:"expires_at"`
}

// ContainerSnapshotPut represents the modifiable fields of a LXD container snapshot.
//
// API extension: snapshot_expiry
type ContainerSnapshotPut InstanceSnapshotPut

// InstanceSnapshot represents a LXD instance snapshot.
//
// API extension: instances
type InstanceSnapshot struct {
	InstanceSnapshotPut `yaml:",inline"`

	CreatedAt       time.Time                    `json:"created_at" yaml:"created_at"`
	ExpandedConfig  map[string]string            `json:"expanded_config" yaml:"expanded_config"`
	ExpandedDevices map[string]map[string]string `json:"expanded_devices" yaml:"expanded_devices"`
	LastUsedAt      time.Time                    `json:"last_used_at" yaml:"last_used_at"`
	Name            string                       `json:"name" yaml:"name"`
	Stateful        bool                         `json:"stateful" yaml:"stateful"`
}

// ContainerSnapshot represents a LXD container snapshot.
type ContainerSnapshot InstanceSnapshot

// Writable converts a full InstanceSnapshot struct into a InstanceSnapshotPut struct.
// (filters read-only fields)
func (c *InstanceSnapshot) Writable() InstanceSnapshotPut {
	return c.InstanceSnapshotPut
}

// Writable converts a full ContainerSnapshot struct into a ContainerSnapshotPut struct.
// (filters read-only fields)
func (c *ContainerSnapshot) Writable() ContainerSnapshotPut {
	return ContainerSnapshotPut(c.InstanceSnapshotPut)
}
