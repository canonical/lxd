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
	//
	// API extension: clustering
	Status string `json:"status" yaml:"status"`

	// Storage pool configuration map (refer to doc/storage.md)
	// Example: {"volume.block.filesystem": "ext4", "volume.size": "50GiB"}
	Config map[string]string `json:"config" yaml:"config"`

	// Cluster members on which the storage pool has been defined
	// Read only: true
	// Example: ["lxd01", "lxd02", "lxd03"]
	//
	// API extension: clustering
	Locations []string `json:"locations" yaml:"locations"`
}
