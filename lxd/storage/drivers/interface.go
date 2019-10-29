package drivers

import (
	"io"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/api"
)

// driver is the extended internal interface.
type driver interface {
	Driver

	init(state *state.State, name string, config map[string]string, volIDFunc func(volType VolumeType, volName string) (int64, error), commonRulesFunc func() map[string]func(string) error)
}

// Driver represents a low-level storage driver.
type Driver interface {
	// Internal.
	Info() Info
	HasVolume(volType VolumeType, volName string) bool

	// Pool.
	Create() error
	Delete(op *operations.Operation) error
	Mount() (bool, error)
	Unmount() (bool, error)
	GetResources() (*api.ResourcesStoragePool, error)

	// Volumes.
	ValidateVolume(volConfig map[string]string, removeUnknownKeys bool) error
	CreateVolume(vol Volume, filler func(path string) error, op *operations.Operation) error
	CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, op *operations.Operation) error
	DeleteVolume(volType VolumeType, volName string, op *operations.Operation) error
	RenameVolume(volType VolumeType, volName string, newName string, op *operations.Operation) error

	// MountVolume mounts a storage volume, returns true if we caused a new mount, false if
	// already mounted.
	MountVolume(volType VolumeType, volName string, op *operations.Operation) (bool, error)

	// MountVolumeSnapshot mounts a storage volume snapshot as readonly, returns true if we
	// caused a new mount, false if already mounted.
	MountVolumeSnapshot(volType VolumeType, VolName, snapshotName string, op *operations.Operation) (bool, error)

	// UnmountVolume unmounts a storage volume, returns true if unmounted, false if was not
	// mounted.
	UnmountVolume(volType VolumeType, volName string, op *operations.Operation) (bool, error)

	// UnmountVolume unmounts a storage volume snapshot, returns true if unmounted, false if was
	// not mounted.
	UnmountVolumeSnapshot(VolumeType VolumeType, volName, snapshotName string, op *operations.Operation) (bool, error)

	CreateVolumeSnapshot(volType VolumeType, volName string, newSnapshotName string, op *operations.Operation) error
	DeleteVolumeSnapshot(volType VolumeType, volName string, snapshotName string, op *operations.Operation) error
	RenameVolumeSnapshot(volType VolumeType, volName string, snapshotName string, newSnapshotName string, op *operations.Operation) error
	VolumeSnapshots(volType VolumeType, volName string, op *operations.Operation) ([]string, error)

	// Migration.
	MigrationTypes(contentType ContentType) []migration.Type
	MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs migration.VolumeSourceArgs, op *operations.Operation) error
	CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, op *operations.Operation) error
}
