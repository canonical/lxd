package api

// StorageVolumeBitmap represents a volume bitmap
//
// swagger:model
//
// API extension: storage_volume_block_tracking.
type StorageVolumeBitmap struct {
	// Bitmap name
	// Example: bitmap0
	Name string `json:"name" yaml:"name"`

	// Number of dirty bytes
	// Example: 300
	Count int64 `json:"count" yaml:"count"`

	// Granularity of the dirty bitmap in bytes
	// Example: 32768
	Granularity int `json:"granularity" yaml:"granularity"`

	// Whether the bitmap is in use by an operation
	// Example: true
	Busy bool `json:"busy" yaml:"busy"`
}

// StorageVolumeBitmapsPost represents the fields available for a new volume bitmap
//
// swagger:model
//
// API extension: storage_volume_block_tracking.
type StorageVolumeBitmapsPost struct {
	// Bitmap name
	// Example: bitmap0
	Name string `json:"name" yaml:"name"`
}
