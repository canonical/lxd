package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeStorageVolumeBackup is an instantiated StorageVolumeBackup for convenience.
var TypeStorageVolumeBackup = StorageVolumeBackup{}

// TypeNameStorageVolumeBackup is the TypeName for StorageVolumeBackup entities.
const TypeNameStorageVolumeBackup TypeName = "storage_volume_backup"

// StorageVolumeBackup is an implementation of Type for StorageVolumeBackup entities.
type StorageVolumeBackup struct{}

// RequiresProject returns true for entity type StorageVolumeBackup.
func (t StorageVolumeBackup) RequiresProject() bool {
	return true
}

// Name returns entity.TypeNameStorageVolumeBackup.
func (t StorageVolumeBackup) Name() TypeName {
	return TypeNameStorageVolumeBackup
}

// PathTemplate returns the path template for entity type StorageVolumeBackup.
func (t StorageVolumeBackup) PathTemplate() []string {
	return []string{"storage-pools", pathPlaceholder, "volumes", pathPlaceholder, pathPlaceholder, "backups", pathPlaceholder}
}

// URL returns a URL for entity type StorageVolumeBackup.
func (t StorageVolumeBackup) URL(projectName string, location string, storagePoolName string, storageVolumeType string, storageVolumeName string, backupName string) *api.URL {
	return urlMust(t, projectName, location, storagePoolName, storageVolumeType, storageVolumeName, backupName)
}

// String implements fmt.Stringer for StorageVolumeBackup entities.
func (t StorageVolumeBackup) String() string {
	return string(t.Name())
}
