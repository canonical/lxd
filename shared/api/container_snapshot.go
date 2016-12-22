package api

import (
	"time"
)

// ContainerSnapshotsPost represents the fields available for a new LXD container snapshot
type ContainerSnapshotsPost struct {
	Name     string `json:"name"`
	Stateful bool   `json:"stateful"`
}

// ContainerSnapshotPost represents the fields required to rename/move a LXD container snapshot
type ContainerSnapshotPost struct {
	Name      string `json:"name"`
	Migration bool   `json:"migration"`
}

// ContainerSnapshot represents a LXD conainer snapshot
type ContainerSnapshot struct {
	Architecture    string                       `json:"architecture"`
	Config          map[string]string            `json:"config"`
	CreationDate    time.Time                    `json:"created_at"`
	Devices         map[string]map[string]string `json:"devices"`
	Ephemeral       bool                         `json:"ephemeral"`
	ExpandedConfig  map[string]string            `json:"expanded_config"`
	ExpandedDevices map[string]map[string]string `json:"expanded_devices"`
	LastUsedDate    time.Time                    `json:"last_used_at"`
	Name            string                       `json:"name"`
	Profiles        []string                     `json:"profiles"`
	Stateful        bool                         `json:"stateful"`
}
