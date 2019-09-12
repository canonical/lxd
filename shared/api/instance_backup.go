package api

import "time"

// InstanceBackupsPost represents the fields available for a new LXD instance backup.
//
// API extension: instances
type InstanceBackupsPost struct {
	Name             string    `json:"name" yaml:"name"`
	ExpiresAt        time.Time `json:"expires_at" yaml:"expires_at"`
	ContainerOnly    bool      `json:"container_only" yaml:"container_only"`
	OptimizedStorage bool      `json:"optimized_storage" yaml:"optimized_storage"`
}

// InstanceBackup represents a LXD instance backup.
//
// API extension: instances
type InstanceBackup struct {
	Name             string    `json:"name" yaml:"name"`
	CreatedAt        time.Time `json:"created_at" yaml:"created_at"`
	ExpiresAt        time.Time `json:"expires_at" yaml:"expires_at"`
	ContainerOnly    bool      `json:"container_only" yaml:"container_only"`
	OptimizedStorage bool      `json:"optimized_storage" yaml:"optimized_storage"`
}

// InstanceBackupPost represents the fields available for the renaming of a instance backup.
//
// API extension: instances
type InstanceBackupPost struct {
	Name string `json:"name" yaml:"name"`
}
