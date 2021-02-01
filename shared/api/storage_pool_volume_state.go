package api

// StorageVolumeState represents the live state of the volume.
//
// API extension: storage_volume_state
type StorageVolumeState struct {
	Usage *StorageVolumeStateUsage `json:"usage" yaml:"usage"`
}

// StorageVolumeStateUsage represents the disk usage of a volume.
//
// API extension: storage_volume_state
type StorageVolumeStateUsage struct {
	Used uint64 `json:"used,omitempty" yaml:"used,omitempty"`
}
