package api

import (
	"time"
)

// InstanceTypeContainer defines the instance type value for a container.
const InstanceTypeContainer = "container"

// InstancesPost represents the fields available for a new LXD instance.
//
// API extension: instances
type InstancesPost struct {
	InstancePut `yaml:",inline"`

	Name   string         `json:"name" yaml:"name"`
	Source InstanceSource `json:"source" yaml:"source"`

	InstanceType string `json:"instance_type" yaml:"instance_type"`

	// API extension: instances
	Type string `json:"type" yaml:"type"`
}

// ContainersPost represents the fields available for a new LXD container,
type ContainersPost struct {
	ContainerPut `yaml:",inline"`

	Name   string          `json:"name" yaml:"name"`
	Source ContainerSource `json:"source" yaml:"source"`

	InstanceType string `json:"instance_type" yaml:"instance_type"`

	// API extension: instances
	Type string `json:"type" yaml:"type"`
}

// InstancePost represents the fields required to rename/move a LXD instance.
//
// API extension: instances
type InstancePost struct {
	// Used for renames
	Name string `json:"name" yaml:"name"`

	// Used for migration
	Migration bool `json:"migration" yaml:"migration"`

	// API extension: container_stateless_copy
	Live bool `json:"live" yaml:"live"`

	// API extension: container_only_migration
	ContainerOnly bool `json:"container_only" yaml:"container_only"`

	// API extension: container_push_target
	Target *InstancePostTarget `json:"target" yaml:"target"`
}

// ContainerPost represents the fields required to rename/move a LXD container.
type ContainerPost struct {
	// Used for renames
	Name string `json:"name" yaml:"name"`

	// Used for migration
	Migration bool `json:"migration" yaml:"migration"`

	// API extension: container_stateless_copy
	Live bool `json:"live" yaml:"live"`

	// API extension: container_only_migration
	ContainerOnly bool `json:"container_only" yaml:"container_only"`

	// API extension: container_push_target
	Target *ContainerPostTarget `json:"target" yaml:"target"`
}

// InstancePostTarget represents the migration target host and operation.
//
// API extension: instances
type InstancePostTarget struct {
	Certificate string            `json:"certificate" yaml:"certificate"`
	Operation   string            `json:"operation,omitempty" yaml:"operation,omitempty"`
	Websockets  map[string]string `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}

// ContainerPostTarget represents the migration target host and operation.
//
// API extension: container_push_target
type ContainerPostTarget InstancePostTarget

// InstancePut represents the modifiable fields of a LXD instance.
//
// API extension: instances
type InstancePut struct {
	Architecture string                       `json:"architecture" yaml:"architecture"`
	Config       map[string]string            `json:"config" yaml:"config"`
	Devices      map[string]map[string]string `json:"devices" yaml:"devices"`
	Ephemeral    bool                         `json:"ephemeral" yaml:"ephemeral"`
	Profiles     []string                     `json:"profiles" yaml:"profiles"`

	// For snapshot restore
	Restore  string `json:"restore,omitempty" yaml:"restore,omitempty"`
	Stateful bool   `json:"stateful" yaml:"stateful"`

	// API extension: entity_description
	Description string `json:"description" yaml:"description"`
}

// ContainerPut represents the modifiable fields of a LXD container.
type ContainerPut InstancePut

// Instance represents a LXD instance.
//
// API extension: instances
type Instance struct {
	InstancePut `yaml:",inline"`

	CreatedAt       time.Time                    `json:"created_at" yaml:"created_at"`
	ExpandedConfig  map[string]string            `json:"expanded_config" yaml:"expanded_config"`
	ExpandedDevices map[string]map[string]string `json:"expanded_devices" yaml:"expanded_devices"`
	Name            string                       `json:"name" yaml:"name"`
	Status          string                       `json:"status" yaml:"status"`
	StatusCode      StatusCode                   `json:"status_code" yaml:"status_code"`

	// API extension: container_last_used_at
	LastUsedAt time.Time `json:"last_used_at" yaml:"last_used_at"`

	// API extension: clustering
	Location string `json:"location" yaml:"location"`

	// API extension: instances
	Type string `json:"type" yaml:"type"`
}

// Writable converts a full Instance struct into a InstancePut struct (filters read-only fields).
func (c *Instance) Writable() InstancePut {
	return c.InstancePut
}

// IsActive checks whether the instance state indicates the instance is active.
func (c Instance) IsActive() bool {
	switch c.StatusCode {
	case Stopped:
		return false
	case Error:
		return false
	default:
		return true
	}
}

// Container represents a LXD container.
type Container Instance

// Writable converts a full Container struct into a ContainerPut struct (filters read-only fields).
func (c *Container) Writable() ContainerPut {
	return ContainerPut(c.InstancePut)
}

// IsActive checks whether the container state indicates the container is active.
func (c Container) IsActive() bool {
	return Instance(c).IsActive()
}

// InstanceFull is a combination of Instance, InstanceBackup, InstanceState and InstanceSnapshot.
//
// API extension: instances
type InstanceFull struct {
	Instance `yaml:",inline"`

	Backups   []InstanceBackup   `json:"backups" yaml:"backups"`
	State     *InstanceState     `json:"state" yaml:"state"`
	Snapshots []InstanceSnapshot `json:"snapshots" yaml:"snapshots"`
}

// ContainerFull is a combination of Container, ContainerBackup, ContainerState and ContainerSnapshot.
//
// API extension: container_full
type ContainerFull struct {
	Container `yaml:",inline"`

	Backups   []ContainerBackup   `json:"backups" yaml:"backups"`
	State     *ContainerState     `json:"state" yaml:"state"`
	Snapshots []ContainerSnapshot `json:"snapshots" yaml:"snapshots"`
}

// InstanceSource represents the creation source for a new instance.
//
// API extension: instances
type InstanceSource struct {
	Type        string `json:"type" yaml:"type"`
	Certificate string `json:"certificate" yaml:"certificate"`

	// For "image" type
	Alias       string            `json:"alias,omitempty" yaml:"alias,omitempty"`
	Fingerprint string            `json:"fingerprint,omitempty" yaml:"fingerprint,omitempty"`
	Properties  map[string]string `json:"properties,omitempty" yaml:"properties,omitempty"`
	Server      string            `json:"server,omitempty" yaml:"server,omitempty"`
	Secret      string            `json:"secret,omitempty" yaml:"secret,omitempty"`
	Protocol    string            `json:"protocol,omitempty" yaml:"protocol,omitempty"`

	// For "migration" and "copy" types
	BaseImage string `json:"base-image,omitempty" yaml:"base-image,omitempty"`

	// For "migration" type
	Mode       string            `json:"mode,omitempty" yaml:"mode,omitempty"`
	Operation  string            `json:"operation,omitempty" yaml:"operation,omitempty"`
	Websockets map[string]string `json:"secrets,omitempty" yaml:"secrets,omitempty"`

	// For "copy" type
	Source string `json:"source,omitempty" yaml:"source,omitempty"`

	// API extension: container_push
	Live bool `json:"live,omitempty" yaml:"live,omitempty"`

	// API extension: container_only_migration
	ContainerOnly bool `json:"container_only,omitempty" yaml:"container_only,omitempty"`

	// API extension: container_incremental_copy
	Refresh bool `json:"refresh,omitempty" yaml:"refresh,omitempty"`

	// API extension: container_copy_project
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
}

// ContainerSource represents the creation source for a new container.
type ContainerSource InstanceSource
