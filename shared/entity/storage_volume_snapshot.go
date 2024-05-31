package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeStorageVolumeSnapshot is an instantiated StorageVolumeSnapshot for convenience.
var TypeStorageVolumeSnapshot = StorageVolumeSnapshot{}

// TypeNameStorageVolumeSnapshot is the TypeName for StorageVolumeSnapshot entities.
const TypeNameStorageVolumeSnapshot TypeName = "storage_volume_snapshot"

// StorageVolumeSnapshot is an implementation of Type for StorageVolumeSnapshot entities.
type StorageVolumeSnapshot struct{}

// RequiresProject returns true for entity type StorageVolumeSnapshot.
func (t StorageVolumeSnapshot) RequiresProject() bool {
	return true
}

// Name returns entity.TypeNameStorageVolumeSnapshot.
func (t StorageVolumeSnapshot) Name() TypeName {
	return TypeNameStorageVolumeSnapshot
}

// PathTemplate returns the path template for entity type StorageVolumeSnapshot.
func (t StorageVolumeSnapshot) PathTemplate() []string {
	return []string{"storage-pools", pathPlaceholder, "volumes", pathPlaceholder, pathPlaceholder, "snapshots", pathPlaceholder}
}

// URL returns a URL for entity type StorageVolumeSnapshot.
func (t StorageVolumeSnapshot) URL(projectName string, location string, storagePoolName string, storageVolumeType string, storageVolumeName string, snapshotName string) *api.URL {
	return urlMust(t, projectName, location, storagePoolName, storageVolumeType, storageVolumeName, snapshotName)
}

// String implements fmt.Stringer for StorageVolumeSnapshot entities.
func (t StorageVolumeSnapshot) String() string {
	return string(t.Name())
}
