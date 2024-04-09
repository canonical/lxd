package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeStorageVolume is an instantiated StorageVolume for convenience.
var TypeStorageVolume = StorageVolume{}

// TypeNameStorageVolume is the TypeName for StorageVolume entities.
const TypeNameStorageVolume TypeName = "storage_volume"

// StorageVolume is an implementation of Type for StorageVolume entities.
type StorageVolume struct{}

// RequiresProject returns true for entity type StorageVolume.
func (t StorageVolume) RequiresProject() bool {
	return true
}

// Name returns entity.TypeNameStorageVolume.
func (t StorageVolume) Name() TypeName {
	return TypeNameStorageVolume
}

// PathTemplate returns the path template for entity type StorageVolume.
func (t StorageVolume) PathTemplate() []string {
	return []string{"storage-pools", pathPlaceholder, "volumes", pathPlaceholder, pathPlaceholder}
}

// URL returns a URL for entity type StorageVolume.
func (t StorageVolume) URL(projectName string, location string, storagePoolName string, storageVolumeType string, storageVolumeName string) *api.URL {
	return urlMust(t, projectName, location, storagePoolName, storageVolumeType, storageVolumeName)
}

// String implements fmt.Stringer for StorageVolume entities.
func (t StorageVolume) String() string {
	return string(t.Name())
}
