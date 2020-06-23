package api

import "time"

// StorageVolumeSnapshotsPost represents the fields available for a new LXD storage volume snapshot
//
// API extension: storage_api_volume_snapshots
type StorageVolumeSnapshotsPost struct {
	Name string `json:"name" yaml:"name"`

	// API extension: custom_volume_snapshot_expiry
	ExpiresAt *time.Time `json:"expires_at" yaml:"expires_at"`
}

// StorageVolumeSnapshotPost represents the fields required to rename/move a LXD storage volume snapshot
//
// API extension: storage_api_volume_snapshots
type StorageVolumeSnapshotPost struct {
	Name string `json:"name" yaml:"name"`
}

// StorageVolumeSnapshot represents a LXD storage volume snapshot
//
// API extension: storage_api_volume_snapshots
type StorageVolumeSnapshot struct {
	StorageVolumeSnapshotPut `json:",inline" yaml:",inline"`

	Name   string            `json:"name" yaml:"name"`
	Config map[string]string `json:"config" yaml:"config"`

	// API extension: custom_block_volumes
	ContentType string `json:"content_type" yaml:"content_type"`
}

// StorageVolumeSnapshotPut represents the modifiable fields of a LXD storage volume
//
// API extension: storage_api_volume_snapshots
type StorageVolumeSnapshotPut struct {
	Description string `json:"description" yaml:"description"`

	// API extension: custom_volume_snapshot_expiry
	ExpiresAt *time.Time `json:"expires_at" yaml:"expires_at"`
}
