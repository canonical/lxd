package config

import (
	"github.com/canonical/lxd/shared/api"
)

// DefaultMetadataVersion represents the current default version of the format used when writing a backup's metadata.
// The metadata is used both for exporting backups and for migration.
const DefaultMetadataVersion = api.BackupMetadataVersion1

// MaxMetadataVersion represents the latest supported metadata version.
const MaxMetadataVersion = api.BackupMetadataVersion2

// Config represents the config of a backup that can be stored in a backup.yaml file (or embedded in index.yaml).
type Config struct {
	Container       *api.Instance                `yaml:"container,omitempty"` // Used by VM backups too.
	Snapshots       []*api.InstanceSnapshot      `yaml:"snapshots,omitempty"`
	Pool            *api.StoragePool             `yaml:"pool,omitempty"`
	Profiles        []*api.Profile               `yaml:"profiles,omitempty"`
	Volume          *api.StorageVolume           `yaml:"volume,omitempty"`
	VolumeSnapshots []*api.StorageVolumeSnapshot `yaml:"volume_snapshots,omitempty"`
	Bucket          *api.StorageBucket           `yaml:"bucket,omitempty"`
}
