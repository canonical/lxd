package api

import (
	"time"
)

// ContainerSnapshotsPost represents the fields available for a new LXD container snapshot.
type ContainerSnapshotsPost struct {
	Name     string `json:"name"     yaml:"name"`
	Stateful bool   `json:"stateful" yaml:"stateful"`

	// API extension: snapshot_expiry_creation
	ExpiresAt *time.Time `json:"expires_at" yaml:"expires_at"`
}

// ContainerSnapshotPost represents the fields required to rename/move a LXD container snapshot.
type ContainerSnapshotPost struct {
	Name      string               `json:"name"      yaml:"name"`
	Migration bool                 `json:"migration" yaml:"migration"`
	Target    *ContainerPostTarget `json:"target"    yaml:"target"`

	// API extension: container_snapshot_stateful_migration
	Live bool `json:"live,omitempty" yaml:"live,omitempty"`
}

// ContainerSnapshotPut represents the modifiable fields of a LXD container snapshot
// API extension: snapshot_expiry.
type ContainerSnapshotPut struct {
	Architecture string                       `json:"architecture" yaml:"architecture"`
	Config       map[string]string            `json:"config"       yaml:"config"`
	Devices      map[string]map[string]string `json:"devices"      yaml:"devices"`
	Ephemeral    bool                         `json:"ephemeral"    yaml:"ephemeral"`
	Profiles     []string                     `json:"profiles"     yaml:"profiles"`
	ExpiresAt    time.Time                    `json:"expires_at"   yaml:"expires_at"`
}

// ContainerSnapshot represents a LXD conainer snapshot.
type ContainerSnapshot struct {
	Name            string                       `json:"name"             yaml:"name"`
	Stateful        bool                         `json:"stateful"         yaml:"stateful"`
	Ephemeral       bool                         `json:"ephemeral"        yaml:"ephemeral"`
	Architecture    string                       `json:"architecture"     yaml:"architecture"`
	CreatedAt       time.Time                    `json:"created_at"       yaml:"created_at"`
	ExpiresAt       time.Time                    `json:"expires_at"       yaml:"expires_at"`
	LastUsedAt      time.Time                    `json:"last_used_at"     yaml:"last_used_at"`
	Profiles        []string                     `json:"profiles"         yaml:"profiles"`
	Config          map[string]string            `json:"config"           yaml:"config"`
	Devices         map[string]map[string]string `json:"devices"          yaml:"devices"`
	ExpandedConfig  map[string]string            `json:"expanded_config"  yaml:"expanded_config"`
	ExpandedDevices map[string]map[string]string `json:"expanded_devices" yaml:"expanded_devices"`
}

// Writable converts a full ContainerSnapshot struct into a ContainerSnapshotPut struct
// (filters read-only fields).
func (c *ContainerSnapshot) Writable() ContainerSnapshotPut {
	return ContainerSnapshotPut{
		Architecture: c.Architecture,
		Ephemeral:    c.Ephemeral,
		ExpiresAt:    c.ExpiresAt,
		Profiles:     c.Profiles,
		Config:       c.Config,
		Devices:      c.Devices,
	}
}

// SetWritable sets applicable values from ContainerSnapshotPut struct to ContainerSnapshot struct.
func (c *ContainerSnapshot) SetWritable(put ContainerSnapshotPut) {
	c.Architecture = put.Architecture
	c.Ephemeral = put.Ephemeral
	c.ExpiresAt = put.ExpiresAt
	c.Profiles = put.Profiles
	c.Config = put.Config
	c.Devices = put.Devices
}
