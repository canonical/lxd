package api

// StorageVolumeNBDGet represents the fields available for a read-only NBD export of a volume
//
// swagger:model
//
// API extension: storage_volume_block_tracking.
type StorageVolumeNBDGet struct {
	// Attach to the NBD session already open for the volume instead of opening a new one
	// Example: false
	Reuse bool `json:"reuse" yaml:"reuse"`
}

// StorageVolumeNBDPost represents the fields available for a writable NBD import of a volume
//
// swagger:model
//
// API extension: storage_volume_block_tracking.
type StorageVolumeNBDPost struct{}
