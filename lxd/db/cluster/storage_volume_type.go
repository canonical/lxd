package cluster

import (
	"errors"
)

// StoragePoolVolumeType records a volume's type in the database.
//
// # Type Safety
// Funtions using this type should assume that a StoragePoolVolumeType is always
// a valid value; i.e. that it is type safe. Use the parsing methods below when
// converting to StoragePoolVolumeType.
type StoragePoolVolumeType int

// XXX: this was extracted from lxd/storage_volume_utils.go, we find a way to
// factor it independently from both the db and main packages.
const (
	StoragePoolVolumeTypeContainer StoragePoolVolumeType = iota
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

// StoragePoolVolumeTypeFromInt is a checked conversion to StoragePoolVolumeType.
func StoragePoolVolumeTypeFromInt(volType int) (StoragePoolVolumeType, error) {
	switch StoragePoolVolumeType(volType) {
	case StoragePoolVolumeTypeContainer, StoragePoolVolumeTypeVM, StoragePoolVolumeTypeCustom, StoragePoolVolumeTypeImage:
		return StoragePoolVolumeType(volType), nil
	default:
		return StoragePoolVolumeType(volType), errors.New("Invalid storage volume type")
	}
}

// StoragePoolVolumeTypeFromName is a checked conversion to StoragePoolVolumeType.
func StoragePoolVolumeTypeFromName(volTypeName string) (StoragePoolVolumeType, error) {
	switch volTypeName {
	case StoragePoolVolumeTypeNameContainer:
		return StoragePoolVolumeTypeContainer, nil
	case StoragePoolVolumeTypeNameVM:
		return StoragePoolVolumeTypeVM, nil
	case StoragePoolVolumeTypeNameImage:
		return StoragePoolVolumeTypeImage, nil
	case StoragePoolVolumeTypeNameCustom:
		return StoragePoolVolumeTypeCustom, nil
	}

	return StoragePoolVolumeTypeCustom, errors.New("Invalid storage volume type")
}

// Name gives the name of a StoragePoolVolumeType.
//
// # Safety
// This function assumes that `t` is one of the StoragePoolVolumeType enums
// defined above.
func (t StoragePoolVolumeType) Name() string {
	switch t {
	case StoragePoolVolumeTypeContainer:
		return StoragePoolVolumeTypeNameContainer
	case StoragePoolVolumeTypeVM:
		return StoragePoolVolumeTypeNameVM
	case StoragePoolVolumeTypeImage:
		return StoragePoolVolumeTypeNameImage
	case StoragePoolVolumeTypeCustom:
		return StoragePoolVolumeTypeNameCustom
	}

	return StoragePoolVolumeTypeNameCustom
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
