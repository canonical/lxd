package api

import (
	"time"
)

// ContainerSnapshotsPost represents the fields available for a new LXD container snapshot
type ContainerSnapshotsPost struct {
	Name     string `json:"name" yaml:"name"`
	Stateful bool   `json:"stateful" yaml:"stateful"`
}

// ContainerSnapshotPost represents the fields required to rename/move a LXD container snapshot
type ContainerSnapshotPost struct {
	Name      string               `json:"name" yaml:"name"`
	Migration bool                 `json:"migration" yaml:"migration"`
	Target    *ContainerPostTarget `json:"target" yaml:"target"`

	// API extension: container_snapshot_stateful_migration
	Live bool `json:"live,omitempty" yaml:"live,omitempty"`
}

// ContainerSnapshot represents a LXD conainer snapshot
type ContainerSnapshot struct {
	Architecture    string                       `json:"architecture" yaml:"architecture"`
	Config          map[string]string            `json:"config" yaml:"config"`
	CreationDate    time.Time                    `json:"created_at" yaml:"created_at"`
	Devices         map[string]map[string]string `json:"devices" yaml:"devices"`
	Ephemeral       bool                         `json:"ephemeral" yaml:"ephemeral"`
	ExpandedConfig  map[string]string            `json:"expanded_config" yaml:"expanded_config"`
	ExpandedDevices map[string]map[string]string `json:"expanded_devices" yaml:"expanded_devices"`
	LastUsedDate    time.Time                    `json:"last_used_at" yaml:"last_used_at"`
	Name            string                       `json:"name" yaml:"name"`
	Profiles        []string                     `json:"profiles" yaml:"profiles"`
	Stateful        bool                         `json:"stateful" yaml:"stateful"`
}
