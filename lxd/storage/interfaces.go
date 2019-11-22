package storage

import (
	"io"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
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

	IsRunning() bool
	IsSnapshot() bool
	DeferTemplateApply(trigger string) error
	ExpandedDevices() deviceConfig.Devices
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
	Delete(localOnly bool, op *operations.Operation) error

	Mount() (bool, error)
	Unmount() (bool, error)

	// Instances.
	CreateInstance(inst Instance, op *operations.Operation) error
	CreateInstanceFromBackup(inst Instance, sourcePath string, op *operations.Operation) error
	CreateInstanceFromCopy(inst Instance, src Instance, snapshots bool, op *operations.Operation) error
	CreateInstanceFromImage(inst Instance, fingerprint string, op *operations.Operation) error
	CreateInstanceFromMigration(inst Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error
	RenameInstance(inst Instance, newName string, op *operations.Operation) error
	DeleteInstance(inst Instance, op *operations.Operation) error

	MigrateInstance(inst Instance, conn io.ReadWriteCloser, args migration.VolumeSourceArgs, op *operations.Operation) error
	RefreshInstance(inst instance.Instance, src instance.Instance, srcSnapshots []instance.Instance, op *operations.Operation) error
	BackupInstance(inst Instance, targetPath string, optimized bool, snapshots bool, op *operations.Operation) error

	GetInstanceUsage(inst Instance) (int64, error)
	SetInstanceQuota(inst Instance, size string, op *operations.Operation) error

	MountInstance(inst Instance, op *operations.Operation) (bool, error)
	UnmountInstance(inst Instance, op *operations.Operation) (bool, error)
	GetInstanceDisk(inst Instance) (string, error)

	// Instance snapshots.
	CreateInstanceSnapshot(inst Instance, name string, op *operations.Operation) error
	RenameInstanceSnapshot(inst Instance, newName string, op *operations.Operation) error
	DeleteInstanceSnapshot(inst Instance, op *operations.Operation) error
	RestoreInstanceSnapshot(inst Instance, op *operations.Operation) error
	MountInstanceSnapshot(inst Instance, op *operations.Operation) (bool, error)
	UnmountInstanceSnapshot(inst Instance, op *operations.Operation) (bool, error)

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
