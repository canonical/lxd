package api

import "time"

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
