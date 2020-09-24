package api

import "time"

// StorageVolumesPost represents the fields of a new LXD storage pool volume
//
// API extension: storage
type StorageVolumesPost struct {
	StorageVolumePut `yaml:",inline"`

	Name string `json:"name" yaml:"name"`
	Type string `json:"type" yaml:"type"`

	// API extension: storage_api_local_volume_handling
	Source StorageVolumeSource `json:"source" yaml:"source"`

	// API extension: custom_block_volumes
	ContentType string `json:"content_type" yaml:"content_type"`
}

// StorageVolumePost represents the fields required to rename a LXD storage pool volume
//
// API extension: storage_api_volume_rename
type StorageVolumePost struct {
	Name string `json:"name" yaml:"name"`

	// API extension: storage_api_local_volume_handling
	Pool string `json:"pool,omitempty" yaml:"pool,omitempty"`

	// API extension: storage_api_remote_volume_handling
	Migration bool `json:"migration" yaml:"migration"`

	// API extension: storage_api_remote_volume_handling
	Target *StorageVolumePostTarget `json:"target" yaml:"target"`

	// API extension: storage_api_remote_volume_snapshots
	VolumeOnly bool `json:"volume_only" yaml:"volume_only"`
}

// StorageVolumePostTarget represents the migration target host and operation
//
// API extension: storage_api_remote_volume_handling
type StorageVolumePostTarget struct {
	Certificate string            `json:"certificate" yaml:"certificate"`
	Operation   string            `json:"operation,omitempty" yaml:"operation,omitempty"`
	Websockets  map[string]string `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}

// StorageVolume represents the fields of a LXD storage volume.
//
// API extension: storage
type StorageVolume struct {
	StorageVolumePut `yaml:",inline"`
	Name             string   `json:"name" yaml:"name"`
	Type             string   `json:"type" yaml:"type"`
	UsedBy           []string `json:"used_by" yaml:"used_by"`

	// API extension: clustering
	Location string `json:"location" yaml:"location"`

	// API extension: custom_block_volumes
	ContentType string `json:"content_type" yaml:"content_type"`
}

// StorageVolumePut represents the modifiable fields of a LXD storage volume.
//
// API extension: storage
type StorageVolumePut struct {
	Config map[string]string `json:"config" yaml:"config"`

	// API extension: entity_description
	Description string `json:"description" yaml:"description"`

	// API extension: storage_api_volume_snapshots
	Restore string `json:"restore,omitempty" yaml:"restore,omitempty"`
}

// StorageVolumeSource represents the creation source for a new storage volume.
//
// API extension: storage_api_local_volume_handling
type StorageVolumeSource struct {
	Name string `json:"name" yaml:"name"`
	Type string `json:"type" yaml:"type"`
	Pool string `json:"pool" yaml:"pool"`

	// API extension: storage_api_remote_volume_handling
	Certificate string            `json:"certificate" yaml:"certificate"`
	Mode        string            `json:"mode,omitempty" yaml:"mode,omitempty"`
	Operation   string            `json:"operation,omitempty" yaml:"operation,omitempty"`
	Websockets  map[string]string `json:"secrets,omitempty" yaml:"secrets,omitempty"`

	// API extension: storage_api_volume_snapshots
	VolumeOnly bool `json:"volume_only" yaml:"volume_only"`
}

// StoragePoolVolumeBackup represents a LXD volume backup.
//
// API extension: custom_volume_backup
type StoragePoolVolumeBackup struct {
	Name             string    `json:"name" yaml:"name"`
	CreatedAt        time.Time `json:"created_at" yaml:"created_at"`
	ExpiresAt        time.Time `json:"expires_at" yaml:"expires_at"`
	VolumeOnly       bool      `json:"volume_only" yaml:"volume_only"`
	OptimizedStorage bool      `json:"optimized_storage" yaml:"optimized_storage"`
}

// StoragePoolVolumeBackupsPost represents the fields available for a new LXD volume backup.
//
// API extension: custom_volume_backup
type StoragePoolVolumeBackupsPost struct {
	Name                 string    `json:"name" yaml:"name"`
	ExpiresAt            time.Time `json:"expires_at" yaml:"expires_at"`
	VolumeOnly           bool      `json:"volume_only" yaml:"volume_only"`
	OptimizedStorage     bool      `json:"optimized_storage" yaml:"optimized_storage"`
	CompressionAlgorithm string    `json:"compression_algorithm" yaml:"compression_algorithm"`
}

// StoragePoolVolumeBackupPost represents the fields available for the renaming of a volume backup.
//
// API extension: custom_volume_backup
type StoragePoolVolumeBackupPost struct {
	Name string `json:"name" yaml:"name"`
}

// Writable converts a full StorageVolume struct into a StorageVolumePut struct
// (filters read-only fields).
func (storageVolume *StorageVolume) Writable() StorageVolumePut {
	return storageVolume.StorageVolumePut
}
