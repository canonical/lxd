package api

import (
	"time"
)

const (
	// ReplicatorProjectModeLeader indicates the project is the active source for replication.
	ReplicatorProjectModeLeader = "leader"

	// ReplicatorProjectModeStandby indicates the project is the passive replication target.
	ReplicatorProjectModeStandby = "standby"
)

// Replicator represents high-level information about a replicator.
//
// swagger:model
//
// API extension: replicators.
type Replicator struct {
	WithEntitlements `yaml:",inline"`

	// Name of the replicator.
	// Example: lxd02
	Name string `json:"name" yaml:"name"`

	// Description of the replicator.
	// Example: Backup LXD cluster
	Description string `json:"description" yaml:"description"`

	// Source project for replication.
	// Example: default
	Project string `json:"project" yaml:"project"`

	// Replicator configuration map (refer to doc/reference/replicator_config.md).
	// Example: {"schedule": "@daily"}
	Config map[string]string `json:"config" yaml:"config"`

	// Timestamp when the replicator job was last run.
	// Example: 2021-03-23T17:38:37.753398689-04:00
	LastRunAt time.Time `json:"last_run_at" yaml:"last_run_at"`

	// Status of the last replicator run (Pending, Completed, or Failed).
	// Example: Completed
	LastRunStatus string `json:"last_run_status" yaml:"last_run_status"`
}

// ReplicatorPut represents the modifiable fields of a replicator.
//
// swagger:model
//
// API extension: replicators.
type ReplicatorPut struct {
	// Description of the replicator.
	// Example: Backup cluster.
	Description string `json:"description" yaml:"description"`

	// Replicator configuration map (refer to doc/reference/replicator_config.md).
	// Example: {"schedule": "@daily"}
	Config map[string]string `json:"config" yaml:"config"`
}

// ReplicatorsPost represents the fields available for a new replicator.
//
// swagger:model
//
// API extension: replicators.
type ReplicatorsPost struct {
	ReplicatorPut `yaml:",inline"`

	// Name of the replicator.
	// Example: backup-lxd02
	Name string `json:"name" yaml:"name"`
}

// ReplicatorPost represents the fields for renaming a replicator.
//
// swagger:model
//
// API extension: replicators.
type ReplicatorPost struct {
	// New name of the replicator.
	// Example: my-replicator-renamed
	Name string `json:"name" yaml:"name"`
}

// Writable converts a full Replicator struct into a [ReplicatorPut] struct (filters read-only fields).
func (replicator *Replicator) Writable() ReplicatorPut {
	return ReplicatorPut{
		Description: replicator.Description,
		Config:      replicator.Config,
	}
}

// SetWritable sets applicable values from [ReplicatorPut] struct to [Replicator] struct.
func (replicator *Replicator) SetWritable(put ReplicatorPut) {
	replicator.Description = put.Description
	replicator.Config = put.Config
}
