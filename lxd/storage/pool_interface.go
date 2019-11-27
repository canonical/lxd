package storage

import (
	"io"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared/api"
)

// Pool represents a LXD storage pool.
type Pool interface {
	// Internal.
	DaemonState() *state.State

	// Pool.
	ID() int64
	Name() string
	Driver() drivers.Driver

	GetResources() (*api.ResourcesStoragePool, error)
	Delete(localOnly bool, op *operations.Operation) error

	Mount() (bool, error)
	Unmount() (bool, error)

	// Instances.
	CreateInstance(inst instance.Instance, op *operations.Operation) error
	CreateInstanceFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (func(instance.Instance) error, func(), error)
	CreateInstanceFromCopy(inst instance.Instance, src instance.Instance, snapshots bool, op *operations.Operation) error
	CreateInstanceFromImage(inst instance.Instance, fingerprint string, op *operations.Operation) error
	CreateInstanceFromMigration(inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error
	RenameInstance(inst instance.Instance, newName string, op *operations.Operation) error
	DeleteInstance(inst instance.Instance, op *operations.Operation) error

	MigrateInstance(inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeSourceArgs, op *operations.Operation) error
	RefreshInstance(inst instance.Instance, src instance.Instance, srcSnapshots []instance.Instance, op *operations.Operation) error
	BackupInstance(inst instance.Instance, targetPath string, optimized bool, snapshots bool, op *operations.Operation) error

	GetInstanceUsage(inst instance.Instance) (int64, error)
	SetInstanceQuota(inst instance.Instance, size string, op *operations.Operation) error

	MountInstance(inst instance.Instance, op *operations.Operation) (bool, error)
	UnmountInstance(inst instance.Instance, op *operations.Operation) (bool, error)
	GetInstanceDisk(inst instance.Instance) (string, error)

	// Instance snapshots.
	CreateInstanceSnapshot(inst instance.Instance, src instance.Instance, op *operations.Operation) error
	RenameInstanceSnapshot(inst instance.Instance, newName string, op *operations.Operation) error
	DeleteInstanceSnapshot(inst instance.Instance, op *operations.Operation) error
	RestoreInstanceSnapshot(inst instance.Instance, src instance.Instance, op *operations.Operation) error
	MountInstanceSnapshot(inst instance.Instance, op *operations.Operation) (bool, error)
	UnmountInstanceSnapshot(inst instance.Instance, op *operations.Operation) (bool, error)

	// Images.
	EnsureImage(fingerprint string, op *operations.Operation) error
	DeleteImage(fingerprint string, op *operations.Operation) error

	// Custom volumes.
	CreateCustomVolume(volName, desc string, config map[string]string, op *operations.Operation) error
	CreateCustomVolumeFromCopy(volName, desc string, config map[string]string, srcPoolName, srcVolName string, srcVolOnly bool, op *operations.Operation) error
	UpdateCustomVolume(volName, newDesc string, newConfig map[string]string, op *operations.Operation) error
	RenameCustomVolume(volName string, newVolName string, op *operations.Operation) error
	DeleteCustomVolume(volName string, op *operations.Operation) error
	GetCustomVolumeUsage(volName string) (int64, error)
	MountCustomVolume(volName string, op *operations.Operation) (bool, error)
	UnmountCustomVolume(volName string, op *operations.Operation) (bool, error)

	// Custom volume snapshots.
	CreateCustomVolumeSnapshot(volName string, newSnapshotName string, op *operations.Operation) error
	RenameCustomVolumeSnapshot(volName string, newSnapshotName string, op *operations.Operation) error
	DeleteCustomVolumeSnapshot(volName string, op *operations.Operation) error
	RestoreCustomVolume(volName string, snapshotName string, op *operations.Operation) error

	// Custom volume migration.
	MigrationTypes(contentType drivers.ContentType) []migration.Type
	CreateCustomVolumeFromMigration(conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error
	MigrateCustomVolume(conn io.ReadWriteCloser, args migration.VolumeSourceArgs, op *operations.Operation) error
}
