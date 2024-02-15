package cluster

// XXX: this was extracted from lxd/storage_volume_utils.go, we find a way to
// factor it independently from both the db and main packages.
const (
	StoragePoolVolumeTypeContainer = iota
	StoragePoolVolumeTypeImage
	StoragePoolVolumeTypeCustom
	StoragePoolVolumeTypeVM
)

// Leave the string type in here! This guarantees that go treats this is as a
// typed string constant. Removing it causes go to treat these as untyped string
// constants which is not what we want.
const (
	StoragePoolVolumeTypeNameContainer string = "container"
	StoragePoolVolumeTypeNameVM        string = "virtual-machine"
	StoragePoolVolumeTypeNameImage     string = "image"
	StoragePoolVolumeTypeNameCustom    string = "custom"
)

// StoragePoolVolumeTypeNames represents a map of storage volume types and their names.
var StoragePoolVolumeTypeNames = map[int]string{
	StoragePoolVolumeTypeContainer: "container",
	StoragePoolVolumeTypeImage:     "image",
	StoragePoolVolumeTypeCustom:    "custom",
	StoragePoolVolumeTypeVM:        "virtual-machine",
}

// Content types.
const (
	StoragePoolVolumeContentTypeFS = iota
	StoragePoolVolumeContentTypeBlock
	StoragePoolVolumeContentTypeISO
)

// Content type names.
const (
	StoragePoolVolumeContentTypeNameFS    string = "filesystem"
	StoragePoolVolumeContentTypeNameBlock string = "block"
	StoragePoolVolumeContentTypeNameISO   string = "iso"
)
