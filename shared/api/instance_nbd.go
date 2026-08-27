package api

// InstanceNBDGet represents the fields available for a read-only NBD export of an instance's disks
//
// swagger:model
//
// API extension: storage_volume_block_tracking.
type InstanceNBDGet struct {
	// Attach to the NBD session already open for the instance instead of opening a new one
	// Example: false
	Reuse bool `json:"reuse" yaml:"reuse"`
}
