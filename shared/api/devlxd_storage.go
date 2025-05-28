package api

// DevLXDStoragePool is a devLXD representation of LXD storage pool.
type DevLXDStoragePool struct {
	// Storage pool name
	// Example: local
	Name string `json:"name" yaml:"name"`

	// Storage pool driver
	// Example: zfs
	Driver string `json:"driver" yaml:"driver"`

	// Pool status (Pending, Created, Errored or Unknown)
	// Read only: true
	// Example: Created
	Status string `json:"status" yaml:"status"`
}
