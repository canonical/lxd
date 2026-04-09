package api

// InstanceBitmap represents a dirty bitmap of the block volumes of an instance
//
// swagger:model
//
// Feature gate: changed_block_tracking
// API extension: storage_volume_block_tracking.
type InstanceBitmap struct {
	// Name of the snapshot the bitmap was created with
	// Example: snap0
	Name string `json:"name" yaml:"name"`

	// UUID of the instance snapshot the bitmap was created with, shared by every volume of the snapshot
	// Example: 3d4b4c2c-8a3d-4b4a-9f8e-1c2d3e4f5a6b
	UUID string `json:"uuid" yaml:"uuid"`

	// Volumes the bitmap exists on
	Volumes []InstanceBitmapVolume `json:"volumes" yaml:"volumes"`
}

// InstanceBitmapVolume represents one volume a bitmap of an instance exists on
//
// swagger:model
//
// Feature gate: changed_block_tracking
// API extension: storage_volume_block_tracking.
type InstanceBitmapVolume struct {
	// Storage pool of the volume
	// Example: default
	Pool string `json:"pool" yaml:"pool"`

	// Type of the volume
	// Example: virtual-machine
	Type string `json:"type" yaml:"type"`

	// Name of the volume
	// Example: v1
	Name string `json:"name" yaml:"name"`

	// UUID of the volume, which does not change on a rename
	// Example: 891bd2a3-7d4e-4c5a-9b1f-0e2d3c4b5a6f
	UUID string `json:"uuid" yaml:"uuid"`

	// Name of the disk device the volume was attached through when the snapshot was taken
	// Example: root
	Device string `json:"device" yaml:"device"`

	// Size in bytes of the block represented by one bit of the bitmap
	// Example: 65536
	Granularity int64 `json:"granularity" yaml:"granularity"`

	// Whether the bitmap records writes
	// Example: true
	Recording bool `json:"recording" yaml:"recording"`
}
