package api

import (
	"strings"
	"time"
)

// GetParentAndSnapshotName returns the parent name, snapshot name, and whether it actually was a snapshot name.
func GetParentAndSnapshotName(name string) (parentName string, snapshotName string, isSnapshot bool) {
	fields := strings.SplitN(name, "/", 2)
	if len(fields) == 1 {
		return name, "", false
	}

	return fields[0], fields[1], true
}

// InstanceType represents the type if instance being returned or requested via the API.
type InstanceType string

// InstanceTypeAny defines the instance type value for requesting any instance type.
const InstanceTypeAny = InstanceType("")

// InstanceTypeContainer defines the instance type value for a container.
const InstanceTypeContainer = InstanceType("container")

// InstanceTypeVM defines the instance type value for a virtual-machine.
const InstanceTypeVM = InstanceType("virtual-machine")

// InstancesPost represents the fields available for a new LXD instance.
//
// swagger:model
//
// API extension: instances.
type InstancesPost struct {
	InstancePut `yaml:",inline"`

	// Instance name
	// Example: foo
	Name string `json:"name" yaml:"name"`

	// Creation source
	Source InstanceSource `json:"source" yaml:"source"`

	// Cloud instance type (AWS, GCP, Azure, ...) to emulate with limits
	// Example: t1.micro
	InstanceType string `json:"instance_type" yaml:"instance_type"`

	// Type (container or virtual-machine)
	// Example: container
	Type InstanceType `json:"type" yaml:"type"`
}

// InstancesPut represents the fields available for a mass update.
//
// swagger:model
//
// API extension: instance_bulk_state_change.
type InstancesPut struct {
	// Desired runtime state
	State *InstanceStatePut `json:"state" yaml:"state"`
}

// InstancePost represents the fields required to rename/move a LXD instance.
//
// swagger:model
//
// API extension: instances.
type InstancePost struct {
	// New name for the instance
	// Example: bar
	Name string `json:"name" yaml:"name"`

	// Whether the instance is being migrated to another server
	// Example: false
	Migration bool `json:"migration" yaml:"migration"`

	// Whether to perform a live migration (migration only)
	// Example: false
	Live bool `json:"live" yaml:"live"`

	// Whether snapshots should be discarded (migration only)
	// Example: false
	InstanceOnly bool `json:"instance_only" yaml:"instance_only"`

	// Whether snapshots should be discarded (migration only, deprecated, use instance_only)
	// Example: false
	ContainerOnly bool `json:"container_only" yaml:"container_only"` // Deprecated, use InstanceOnly.

	// Target for the migration, will use pull mode if not set (migration only)
	Target *InstancePostTarget `json:"target" yaml:"target"`

	// Target pool for local cross-pool move
	// Example: baz
	//
	// API extension: instance_pool_move
	Pool string `json:"pool" yaml:"pool"`

	// Target project for local cross-project move
	// Example: foo
	//
	// API extension: instance_project_move
	Project string `json:"project" yaml:"project"`

	// AllowInconsistent allow inconsistent copies when migrating.
	// Example: false
	//
	// API extension: instance_allow_inconsistent_copy
	AllowInconsistent bool `json:"allow_inconsistent" yaml:"allow_inconsistent"`

	// Instance configuration file.
	// Example: {"security.nesting": "true"}
	//
	// API extension: instance_move_config
	Config map[string]string

	// Instance devices.
	// Example: {"root": {"type": "disk", "pool": "default", "path": "/"}}
	//
	// API extension: instance_move_config
	Devices map[string]map[string]string

	// List of profiles applied to the instance.
	// Example: ["default"]
	//
	// API extension: instance_move_config
	Profiles []string
}

// InstancePostTarget represents the migration target host and operation.
//
// swagger:model
//
// API extension: instances.
type InstancePostTarget struct {
	// The certificate of the migration target
	// Example: X509 PEM certificate
	Certificate string `json:"certificate" yaml:"certificate"`

	// The operation URL on the remote target
	// Example: https://1.2.3.4:8443/1.0/operations/5e8e1638-5345-4c2d-bac9-2c79c8577292
	Operation string `json:"operation,omitempty" yaml:"operation,omitempty"`

	// Migration websockets credentials
	// Example: {"migration": "random-string", "criu": "random-string"}
	Websockets map[string]string `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}

// InstancePut represents the modifiable fields of a LXD instance.
//
// swagger:model
//
// API extension: instances.
type InstancePut struct {
	// Architecture name
	// Example: x86_64
	Architecture string `json:"architecture" yaml:"architecture"`

	// Instance configuration (see doc/instances.md)
	// Example: {"security.nesting": "true"}
	Config map[string]string `json:"config" yaml:"config"`

	// Instance devices (see doc/instances.md)
	// Example: {"root": {"type": "disk", "pool": "default", "path": "/"}}
	Devices map[string]map[string]string `json:"devices" yaml:"devices"`

	// Whether the instance is ephemeral (deleted on shutdown)
	// Example: false
	Ephemeral bool `json:"ephemeral" yaml:"ephemeral"`

	// List of profiles applied to the instance
	// Example: ["default"]
	Profiles []string `json:"profiles" yaml:"profiles"`

	// If set, instance will be restored to the provided snapshot name
	// Example: snap0
	Restore string `json:"restore,omitempty" yaml:"restore,omitempty"`

	// Whether the instance currently has saved state on disk
	// Example: false
	Stateful bool `json:"stateful" yaml:"stateful"`

	// Instance description
	// Example: My test instance
	Description string `json:"description" yaml:"description"`
}

// InstanceRebuildPost indicates how to rebuild an instance.
//
// swagger:model
//
// API extension: instances_rebuild.
type InstanceRebuildPost struct {
	// Rebuild source
	Source InstanceSource `json:"source" yaml:"source"`
}

// Instance represents a LXD instance.
//
// swagger:model
//
// API extension: instances.
type Instance struct {
	// Instance name
	// Example: foo
	Name string `json:"name" yaml:"name"`

	// Instance description
	// Example: My test instance
	Description string `json:"description" yaml:"description"`

	// Instance status (see instance_state)
	// Example: Running
	Status string `json:"status" yaml:"status"`

	// Instance status code (see instance_state)
	// Example: 101
	StatusCode StatusCode `json:"status_code" yaml:"status_code"`

	// Instance creation timestamp
	// Example: 2021-03-23T20:00:00-04:00
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`

	// Last start timestamp
	// Example: 2021-03-23T20:00:00-04:00
	LastUsedAt time.Time `json:"last_used_at" yaml:"last_used_at"`

	// What cluster member this instance is located on
	// Example: lxd01
	Location string `json:"location" yaml:"location"`

	// The type of instance (container or virtual-machine)
	// Example: container
	Type string `json:"type" yaml:"type"`

	// Instance project name
	// Example: foo
	//
	// API extension: instance_all_projects
	Project string `json:"project" yaml:"project"`

	// Architecture name
	// Example: x86_64
	Architecture string `json:"architecture" yaml:"architecture"`

	// Whether the instance is ephemeral (deleted on shutdown)
	// Example: false
	Ephemeral bool `json:"ephemeral" yaml:"ephemeral"`

	// Whether the instance currently has saved state on disk
	// Example: false
	Stateful bool `json:"stateful" yaml:"stateful"`

	// List of profiles applied to the instance
	// Example: ["default"]
	Profiles []string `json:"profiles" yaml:"profiles"`

	// Instance configuration (see doc/instances.md)
	// Example: {"security.nesting": "true"}
	Config map[string]string `json:"config" yaml:"config"`

	// Instance devices (see doc/instances.md)
	// Example: {"root": {"type": "disk", "pool": "default", "path": "/"}}
	Devices map[string]map[string]string `json:"devices" yaml:"devices"`

	// Expanded configuration (all profiles and local config merged)
	// Example: {"security.nesting": "true"}
	ExpandedConfig map[string]string `json:"expanded_config,omitempty" yaml:"expanded_config,omitempty"`

	// Expanded devices (all profiles and local devices merged)
	// Example: {"root": {"type": "disk", "pool": "default", "path": "/"}}
	ExpandedDevices map[string]map[string]string `json:"expanded_devices,omitempty" yaml:"expanded_devices,omitempty"`
}

// InstanceFull is a combination of Instance, InstanceBackup, InstanceState and InstanceSnapshot.
//
// swagger:model
//
// API extension: instances.
type InstanceFull struct {
	Instance `yaml:",inline"`

	// List of backups.
	Backups []InstanceBackup `json:"backups" yaml:"backups"`

	// Current state.
	State *InstanceState `json:"state" yaml:"state"`

	// List of snapshots.
	Snapshots []InstanceSnapshot `json:"snapshots" yaml:"snapshots"`
}

// Writable converts a full Instance struct into a InstancePut struct (filters read-only fields).
//
// API extension: instances.
func (c *Instance) Writable() InstancePut {
	return InstancePut{
		Architecture: c.Architecture,
		Config:       c.Config,
		Devices:      c.Devices,
		Ephemeral:    c.Ephemeral,
		Profiles:     c.Profiles,
		Stateful:     c.Stateful,
		Description:  c.Description,
	}
}

// SetWritable sets applicable values from InstancePut struct to Instance struct.
func (c *Instance) SetWritable(put InstancePut) {
	c.Architecture = put.Architecture
	c.Config = put.Config
	c.Devices = put.Devices
	c.Ephemeral = put.Ephemeral
	c.Profiles = put.Profiles
	c.Stateful = put.Stateful
	c.Description = put.Description
}

// IsActive checks whether the instance state indicates the instance is active.
//
// API extension: instances.
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

// URL returns the URL for the instance.
func (c *Instance) URL(apiVersion string, project string) *URL {
	return NewURL().Path(apiVersion, "instances", c.Name).Project(project)
}

// InstanceSource represents the creation source for a new instance.
//
// swagger:model
//
// API extension: instances.
type InstanceSource struct {
	// Source type
	// Example: image
	Type string `json:"type" yaml:"type"`

	// Certificate (for remote images or migration)
	// Example: X509 PEM certificate
	Certificate string `json:"certificate" yaml:"certificate"`

	// Image alias name (for image source)
	// Example: ubuntu/24.04
	Alias string `json:"alias,omitempty" yaml:"alias,omitempty"`

	// Image fingerprint (for image source)
	// Example: ed56997f7c5b48e8d78986d2467a26109be6fb9f2d92e8c7b08eb8b6cec7629a
	Fingerprint string `json:"fingerprint,omitempty" yaml:"fingerprint,omitempty"`

	// Image filters (for image source)
	// Example: {"os": "Ubuntu", "release": "jammy", "variant": "cloud"}
	Properties map[string]string `json:"properties,omitempty" yaml:"properties,omitempty"`

	// Remote server URL (for remote images)
	// Example: https://cloud-images.ubuntu.com/releases
	Server string `json:"server,omitempty" yaml:"server,omitempty"`

	// Remote server secret (for remote private images)
	// Example: RANDOM-STRING
	Secret string `json:"secret,omitempty" yaml:"secret,omitempty"`

	// Protocol name (for remote image)
	// Example: simplestreams
	Protocol string `json:"protocol,omitempty" yaml:"protocol,omitempty"`

	// Base image fingerprint (for faster migration)
	// Example: ed56997f7c5b48e8d78986d2467a26109be6fb9f2d92e8c7b08eb8b6cec7629a
	BaseImage string `json:"base-image,omitempty" yaml:"base-image,omitempty"`

	// Whether to use pull or push mode (for migration)
	// Example: pull
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`

	// Remote operation URL (for migration)
	// Example: https://1.2.3.4:8443/1.0/operations/1721ae08-b6a8-416a-9614-3f89302466e1
	Operation string `json:"operation,omitempty" yaml:"operation,omitempty"`

	// Map of migration websockets (for migration)
	// Example: {"criu": "RANDOM-STRING", "rsync": "RANDOM-STRING"}
	Websockets map[string]string `json:"secrets,omitempty" yaml:"secrets,omitempty"`

	// Existing instance name or snapshot (for copy)
	// Example: foo/snap0
	Source string `json:"source,omitempty" yaml:"source,omitempty"`

	// Whether this is a live migration (for migration)
	// Example: false
	Live bool `json:"live,omitempty" yaml:"live,omitempty"`

	// Whether the copy should skip the snapshots (for copy)
	// Example: false
	InstanceOnly bool `json:"instance_only,omitempty" yaml:"instance_only,omitempty"`

	// Whether the copy should skip the snapshots (for copy, deprecated, use instance_only)
	// Example: false
	ContainerOnly bool `json:"container_only,omitempty" yaml:"container_only,omitempty"` // Deprecated, use InstanceOnly.

	// Whether this is refreshing an existing instance (for migration and copy)
	// Example: false
	Refresh bool `json:"refresh,omitempty" yaml:"refresh,omitempty"`

	// Source project name (for copy and local image)
	// Example: blah
	Project string `json:"project,omitempty" yaml:"project,omitempty"`

	// Whether to ignore errors when copying (e.g. for volatile files)
	// Example: false
	//
	// API extension: instance_allow_inconsistent_copy
	AllowInconsistent bool `json:"allow_inconsistent" yaml:"allow_inconsistent"`

	// Source disk size in bytes used to set the instance's volume size to accommodate the transferred root
	// disk. This value is ignored if the root disk device has a size explicitly configured (for conversion).
	// Example: 12345
	//
	// API extension: instance_import_conversion
	SourceDiskSize int64 `json:"sourceDiskSize" yaml:"sourceDiskSize"`

	// Optional list of options that are used during image conversion (for conversion).
	// Example: ["format"]
	//
	// API extension: instance_import_conversion
	ConversionOptions []string `json:"conversion_options" yaml:"conversion_options"`
}

// InstanceUEFIVars represents the UEFI variables of a LXD virtual machine.
//
// swagger:model
//
// API extension: instances_uefi_vars.
type InstanceUEFIVars struct {
	// UEFI variables map
	// Hashmap key format is <uefi-variable-name>-<UUID>
	// Example: { "SecureBootEnable-f0a30bc7-af08-4556-99c4-001009c93a44": { "data": "01", "attr": 3 } }
	Variables map[string]InstanceUEFIVariable `json:"variables" yaml:"variables"`
}

// InstanceUEFIVariable represents an EFI variable entry
//
// swagger:model
//
// API extension: instances_uefi_vars.
type InstanceUEFIVariable struct {
	// UEFI variable data (HEX-encoded)
	// example: 01
	Data string `json:"data" yaml:"data"`

	// UEFI variable attributes
	// example: 7
	Attr uint32 `json:"attr" yaml:"attr"`

	// UEFI variable timestamp (HEX-encoded)
	Timestamp string `json:"timestamp" yaml:"timestamp"`

	// UEFI variable digest (HEX-encoded)
	Digest string `json:"digest" yaml:"digest"`
}
