package storage

import (
	"io"

	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared/api"
)

// Instance represents the storage relevant subset of a LXD instance.
type Instance interface {
	Name() string
	Project() string
	Type() instancetype.Type
	Path() string

	IsRunning() bool
	Snapshots() ([]Instance, error)
	TemplateApply(trigger string) error
}

// Pool represents a LXD storage pool.
type Pool interface {
	// Internal.
	DaemonState() *state.State

	// Pool.
	ID() int64
	Name() string
	Driver() drivers.Driver

	GetResources() (*api.ResourcesStoragePool, error)
	Delete(op *operations.Operation) error

	Mount() (bool, error)
	Unmount() (bool, error)

	// Instances.
	CreateInstance(i Instance, op *operations.Operation) error
	CreateInstanceFromBackup(i Instance, sourcePath string, op *operations.Operation) error
	CreateInstanceFromCopy(i Instance, src Instance, snapshots bool, op *operations.Operation) error
	CreateInstanceFromImage(i Instance, fingerprint string, op *operations.Operation) error
	CreateInstanceFromMigration(i Instance, conn io.ReadWriteCloser, args migration.SinkArgs, op *operations.Operation) error
	RenameInstance(i Instance, newName string, op *operations.Operation) error
	DeleteInstance(i Instance, op *operations.Operation) error

	MigrateInstance(i Instance, snapshots bool, args migration.SourceArgs) (migration.StorageSourceDriver, error)
	RefreshInstance(i Instance, src Instance, snapshots bool, op *operations.Operation) error
	BackupInstance(i Instance, targetPath string, optimized bool, snapshots bool, op *operations.Operation) error

	GetInstanceUsage(i Instance) (uint64, error)
	SetInstanceQuota(i Instance, quota uint64) error

	MountInstance(i Instance) (bool, error)
	UnmountInstance(i Instance) (bool, error)
	GetInstanceDisk(i Instance) (string, string, error)

	// Instance snapshots.
	CreateInstanceSnapshot(i Instance, name string, op *operations.Operation) error
	RenameInstanceSnapshot(i Instance, newName string, op *operations.Operation) error
	DeleteInstanceSnapshot(i Instance, op *operations.Operation) error
	RestoreInstanceSnapshot(i Instance, op *operations.Operation) error
	MountInstanceSnapshot(i Instance) (bool, error)
	UnmountInstanceSnapshot(i Instance) (bool, error)

	// Images.
	CreateImage(img api.Image, op *operations.Operation) error
	DeleteImage(fingerprint string, op *operations.Operation) error

	// Custom volumes.
	CreateCustomVolume(volName, desc string, config map[string]string, op *operations.Operation) error
	CreateCustomVolumeFromCopy(volName, desc string, config map[string]string, srcPoolName, srcVolName string, srcVolOnly bool, op *operations.Operation) error
	UpdateCustomVolume(volName, newDesc string, newConfig map[string]string, op *operations.Operation) error
	RenameCustomVolume(volName string, newVolName string, op *operations.Operation) error
	DeleteCustomVolume(volName string, op *operations.Operation) error
	GetCustomVolumeUsage(vol api.StorageVolume) (uint64, error)
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
