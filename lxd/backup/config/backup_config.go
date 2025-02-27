package config

import (
	"github.com/canonical/lxd/shared/api"
)

// DefaultMetadataVersion represents the current default version of the format used when writing a backup's metadata.
// The metadata is used both for exporting backups and for migration.
const DefaultMetadataVersion = api.BackupMetadataVersion1

// MaxMetadataVersion represents the latest supported metadata version.
const MaxMetadataVersion = api.BackupMetadataVersion2

// Volume represents the config of a volume including its snapshots.
type Volume struct {
	// Make sure to have the embedded structs fields inline to avoid nesting.
	api.StorageVolume `yaml:",inline"`

	Snapshots []*api.StorageVolumeSnapshot `yaml:"snapshots,omitempty"`
}

// Config represents the config of a backup that can be stored in a backup.yaml file (or embedded in index.yaml).
type Config struct {
	Version   uint32                  `yaml:"version,omitempty"`
	Instance  *api.Instance           `yaml:"instance,omitempty"`
	Snapshots []*api.InstanceSnapshot `yaml:"snapshots,omitempty"`
	Pools     []*api.StoragePool      `yaml:"pools,omitempty"`
	Profiles  []*api.Profile          `yaml:"profiles,omitempty"`
	Volumes   []*Volume               `yaml:"volumes,omitempty"`
	Bucket    *api.StorageBucket      `yaml:"bucket,omitempty"`
	// Deprecated: Use Instance instead.
	Container *api.Instance `yaml:"container,omitempty"`
	// Deprecated: Use Pools instead.
	Pool *api.StoragePool `yaml:"pool,omitempty"`
	// Deprecated: Use Volumes instead.
	Volume *api.StorageVolume `yaml:"volume,omitempty"`
	// Deprecated: Use the list of Snapshots under Volumes.
	VolumeSnapshots []*api.StorageVolumeSnapshot `yaml:"volume_snapshots,omitempty"`
}
