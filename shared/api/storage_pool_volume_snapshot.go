package api

import ()

// StorageVolumeSnapshotPost represents the fields required to rename/move a LXD storage volume snapshot
//
// API extension: storage_api_volume_snapshots
type StorageVolumeSnapshotPost struct {
	Name string `json:"name" yaml:"name"`
	Type string `json:"type" yaml:"type"`
}

// StorageVolumeSnapshot represents a LXD storage volume snapshot
//
// API extension: storage_api_volume_snapshots
type StorageVolumeSnapshot struct {
	Name string `json:"name" yaml:"name"`
	Type string `json:"type" yaml:"type"`
}
