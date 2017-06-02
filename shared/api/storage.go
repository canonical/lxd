package api

// StoragePoolsPost represents the fields of a new LXD storage pool
//
// API extension: storage
type StoragePoolsPost struct {
	StoragePoolPut `yaml:",inline"`

	Name   string `json:"name" yaml:"name"`
	Driver string `json:"driver" yaml:"driver"`
}

// StoragePool represents the fields of a LXD storage pool.
//
// API extension: storage
type StoragePool struct {
	StoragePoolPut `yaml:",inline"`

	Name   string   `json:"name" yaml:"name"`
	Driver string   `json:"driver" yaml:"driver"`
	UsedBy []string `json:"used_by" yaml:"used_by"`
}

// StoragePoolPut represents the modifiable fields of a LXD storage pool.
//
// API extension: storage
type StoragePoolPut struct {
	Config map[string]string `json:"config" yaml:"config"`

	// API extension: entity_description
	Description string `json:"description" yaml:"description"`
}

// StorageVolumesPost represents the fields of a new LXD storage pool volume
//
// API extension: storage
type StorageVolumesPost struct {
	StorageVolumePut `yaml:",inline"`

	Name string `json:"name" yaml:"name"`
	Type string `json:"type" yaml:"type"`
}

// StorageVolume represents the fields of a LXD storage volume.
//
// API extension: storage
type StorageVolume struct {
	StorageVolumePut `yaml:",inline"`
	Name             string   `json:"name" yaml:"name"`
	Type             string   `json:"type" yaml:"type"`
	UsedBy           []string `json:"used_by" yaml:"used_by"`
}

// StorageVolumePut represents the modifiable fields of a LXD storage volume.
//
// API extension: storage
type StorageVolumePut struct {
	Config map[string]string `json:"config" yaml:"config"`

	// API extension: entity_description
	Description string `json:"description" yaml:"description"`
}

// Writable converts a full StoragePool struct into a StoragePoolPut struct
// (filters read-only fields).
func (storagePool *StoragePool) Writable() StoragePoolPut {
	return storagePool.StoragePoolPut
}

// Writable converts a full StorageVolume struct into a StorageVolumePut struct
// (filters read-only fields).
func (storageVolume *StorageVolume) Writable() StorageVolumePut {
	return storageVolume.StorageVolumePut
}
