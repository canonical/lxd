package drivers

import (
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/shared/api"
)

// VolumeType represents a storage volume type.
type VolumeType string

// VolumeTypeCustom represents a custom storage volume.
const VolumeTypeCustom = VolumeType("custom")

// VolumeTypeContainer represents a container storage volume.
const VolumeTypeContainer = VolumeType("containers")

// VolumeTypeImage represents an image storage volume.
const VolumeTypeImage = VolumeType("images")

// VolumeTypeVM represents a virtual-machine storage volume.
const VolumeTypeVM = VolumeType("virtual-machines")

// driver is the extended internal interface
type driver interface {
	Driver

	create(dbPool *api.StoragePool) error
}

// Driver repreents a low-level storage driver.
type Driver interface {
	// Internal
	Name() string
	Version() string

	// Pool
	Delete(op *operations.Operation) error
	Mount() (bool, error)
	Unmount() (bool, error)
	GetResources() (*api.ResourcesStoragePool, error)

	// Volumes
	DeleteVolume(volType VolumeType, name string, op *operations.Operation) error
	RenameVolume(volType VolumeType, name string, newName string, op *operations.Operation) error

	// Migration
	MigrationType() migration.MigrationFSType
	PreservesInodes() bool
}
