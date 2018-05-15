package api

import "time"

// ContainerBackupsPost represents the fields available for a new LXD container backup
// API extension: container_backup
type ContainerBackupsPost struct {
	Name             string    `json:"name" yaml:"name"`
	ExpiryDate       time.Time `json:"expiry" yaml:"expiry"`
	ContainerOnly    bool      `json:"container_only" yaml:"container_only"`
	OptimizedStorage bool      `json:"optimized_storage" yaml:"optimized_storage"`
}

// ContainerBackup represents a LXD container backup
// API extension: container_backup
type ContainerBackup struct {
	Name             string    `json:"name" yaml:"name"`
	CreationDate     time.Time `json:"creation_date" yaml:"creation_date"`
	ExpiryDate       time.Time `json:"expiry_date" yaml:"expiry_date"`
	ContainerOnly    bool      `json:"container_only" yaml:"container_only"`
	OptimizedStorage bool      `json:"optimized_storage" yaml:"optimized_storage"`
}

// ContainerBackupPost represents the fields available for the renaming of a
// container backup
// API extension: container_backup
type ContainerBackupPost struct {
	Name string `json:"name" yaml:"name"`
}
