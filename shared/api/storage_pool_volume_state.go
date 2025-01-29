package api

// StorageVolumeState represents the live state of the volume
//
// swagger:model
//
// API extension: storage_volume_state.
type StorageVolumeState struct {
	// Volume usage
	Usage *StorageVolumeStateUsage `json:"usage" yaml:"usage"`
}

// StorageVolumeStateUsage represents the disk usage of a volume
//
// swagger:model
//
// API extension: storage_volume_state.
type StorageVolumeStateUsage struct {
	// Used space in bytes. Uses 0 to indicate that the storage driver for the pool does not support retrieving volume usage.
	// Example: 1693552640
	Used uint64 `json:"used" yaml:"used"`

	// Storage volume size in bytes. Uses 0 to convey that the volume has access to the entire pool's storage.
	// Example: 5189222192
	//
	// API extension: storage_volume_state_total
	Total int64 `json:"total" yaml:"total"`
}
