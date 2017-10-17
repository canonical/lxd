package api

// StorageVolumesPost represents the fields of a new LXD storage pool volume
//
// API extension: storage
type StorageVolumesPost struct {
	StorageVolumePut `yaml:",inline"`

	Name string `json:"name" yaml:"name"`
	Type string `json:"type" yaml:"type"`
}

// StorageVolumePost represents the fields required to rename a LXD storage pool volume
//
// API extension: storage_api_volume_rename
type StorageVolumePost struct {
	Name string `json:"name" yaml:"name"`
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

// Writable converts a full StorageVolume struct into a StorageVolumePut struct
// (filters read-only fields).
func (storageVolume *StorageVolume) Writable() StorageVolumePut {
	return storageVolume.StorageVolumePut
}
