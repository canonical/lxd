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

// DevLXDStorageVolume is a devLXD representation of LXD storage volume.
type DevLXDStorageVolume struct {
	// Name of the storage volume.
	// Example: my-volume
	Name string `json:"name"`

	// Description of the storage volume.
	// Example: My custom volume
	Description string `json:"description"`

	// Pool name of the storage volume.
	// Example: local
	Pool string `json:"pool"`

	// Type of the storage volume.
	// Example: custom
	Type string `json:"type"`

	// Volume content type (filesystem or block)
	// Example: filesystem
	ContentType string `json:"content_type" yaml:"content_type"`

	// Configuration of the storage volume.
	// Example: {"size": "10GiB", "block.filesystem": "ext4"}
	Config map[string]string `json:"config"`

	// What cluster member this record was found on
	// Example: lxd01
	Location string `json:"location" yaml:"location"`
}

// DevLXDStorageVolumePut represents the modifiable fields of a LXD storage volume
// that can be updated via the devLXD API.
type DevLXDStorageVolumePut struct {
	// Storage volume configuration map (refer to doc/storage.md)
	// Example: {"zfs.remove_snapshots": "true", "size": "50GiB"}
	Config map[string]string `json:"config" yaml:"config"`

	// Description of the storage volume
	// Example: My custom volume
	Description string `json:"description" yaml:"description"`
}

// DevLXDStorageVolumesPost represents the fields of a new LXD storage pool volume
// that can be created via the devLXD API.
type DevLXDStorageVolumesPost struct {
	DevLXDStorageVolumePut `yaml:",inline"`

	// Volume name.
	// Example: foo
	Name string `json:"name" yaml:"name"`

	// Volume type.
	// Example: custom
	Type string `json:"type" yaml:"type"`

	// Volume content type (filesystem or block)
	// Example: filesystem
	ContentType string `json:"content_type" yaml:"content_type"`
}
