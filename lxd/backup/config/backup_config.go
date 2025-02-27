package config

import (
	"github.com/canonical/lxd/shared/api"
)

// VolumeConfig represents the config of a volume including its snapshots.
type VolumeConfig struct {
	// Make sure to have the embedded structs fields inline to avoid nesting.
	api.StorageVolume `yaml:",inline"`
	Snapshots         []*api.StorageVolumeSnapshot `yaml:"snapshots,omitempty"`
}

// Config represents the config of a backup that can be stored in a backup.yaml file (or embedded in index.yaml).
type Config struct {
	Version api.BackupMetadataVersion `yaml:"version,omitempty"`
	// Deprecated.
	// Use Instance instead.
	Container *api.Instance           `yaml:"container,omitempty"`
	Instance  *api.Instance           `yaml:"instance,omitempty"`
	Snapshots []*api.InstanceSnapshot `yaml:"snapshots,omitempty"`
	// Deprecated.
	// Use Pools instead.
	Pool     *api.StoragePool   `yaml:"pool,omitempty"`
	Pools    []*api.StoragePool `yaml:"pools,omitempty"`
	Profiles []*api.Profile     `yaml:"profiles,omitempty"`
	// Deprecated.
	// Use Volumes instead.
	Volume  *api.StorageVolume `yaml:"volume,omitempty"`
	Volumes []*VolumeConfig    `yaml:"volumes,omitempty"`
	// Deprecated.
	// Use the list of Snapshots under Volumes.
	VolumeSnapshots []*api.StorageVolumeSnapshot `yaml:"volume_snapshots,omitempty"`
	Bucket          *api.StorageBucket           `yaml:"bucket,omitempty"`
}
