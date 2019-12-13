package drivers

import (
	"io"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

// driver is the extended internal interface.
type driver interface {
	Driver

	init(state *state.State, name string, config map[string]string, logger logger.Logger, volIDFunc func(volType VolumeType, volName string) (int64, error), commonRulesFunc func() map[string]func(string) error) error
	load() error
}

// Driver represents a low-level storage driver.
type Driver interface {
	// Internal.
	Config() map[string]string
	Info() Info
	HasVolume(vol Volume) bool

	// Pool.
	Create() error
	Delete(op *operations.Operation) error
	// Mount mounts a storage pool if needed, returns true if we caused a new mount, false if
	// already mounted.
	Mount() (bool, error)

	// Unmount unmounts a storage pool if needed, returns true if unmounted, false if was not
	// mounted.
	Unmount() (bool, error)
	GetResources() (*api.ResourcesStoragePool, error)
	Validate(config map[string]string) error
	Update(changedConfig map[string]string) error

	// Volumes.
	ValidateVolume(vol Volume, removeUnknownKeys bool) error
	CreateVolume(vol Volume, filler *VolumeFiller, op *operations.Operation) error
	CreateVolumeFromCopy(vol Volume, srcVol Volume, copySnapshots bool, op *operations.Operation) error
	RefreshVolume(vol Volume, srcVol Volume, srcSnapshots []Volume, op *operations.Operation) error
	DeleteVolume(vol Volume, op *operations.Operation) error
	RenameVolume(vol Volume, newName string, op *operations.Operation) error
	UpdateVolume(vol Volume, changedConfig map[string]string) error
	GetVolumeUsage(vol Volume) (int64, error)
	SetVolumeQuota(vol Volume, size string, op *operations.Operation) error
	GetVolumeDiskPath(vol Volume) (string, error)

	// MountVolume mounts a storage volume, returns true if we caused a new mount, false if
	// already mounted.
	MountVolume(vol Volume, op *operations.Operation) (bool, error)

	// MountVolumeSnapshot mounts a storage volume snapshot as readonly, returns true if we
	// caused a new mount, false if already mounted.
	MountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error)

	// UnmountVolume unmounts a storage volume, returns true if unmounted, false if was not
	// mounted.
	UnmountVolume(vol Volume, op *operations.Operation) (bool, error)

	// UnmountVolume unmounts a storage volume snapshot, returns true if unmounted, false if was
	// not mounted.
	UnmountVolumeSnapshot(snapVol Volume, op *operations.Operation) (bool, error)

	CreateVolumeSnapshot(snapVol Volume, op *operations.Operation) error
	DeleteVolumeSnapshot(snapVol Volume, op *operations.Operation) error
	RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, op *operations.Operation) error
	VolumeSnapshots(vol Volume, op *operations.Operation) ([]string, error)
	RestoreVolume(vol Volume, snapshotName string, op *operations.Operation) error

	// Migration.
	MigrationTypes(contentType ContentType, refresh bool) []migration.Type
	MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs migration.VolumeSourceArgs, op *operations.Operation) error
	CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error

	// Backup.
	BackupVolume(vol Volume, targetPath string, optimized bool, snapshots bool, op *operations.Operation) error
	RestoreBackupVolume(vol Volume, snapshots []string, srcData io.ReadSeeker, optimizedStorage bool, op *operations.Operation) (func(vol Volume) error, func(), error)
}
