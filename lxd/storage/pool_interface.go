package storage

import (
	"io"
	"net/url"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/backup"
	backupConfig "github.com/canonical/lxd/lxd/backup/config"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/storage/s3/miniod"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/revert"
)

// VolumeUsage contains the used and total size of a volume.
type VolumeUsage struct {
	Used  int64
	Total int64
}

// MountInfo represents info about the result of a mount operation.
type MountInfo struct {
	DevSource deviceConfig.DevSource               // The location of the block disk (if supported).
	PostHooks []func(inst instance.Instance) error // Hooks to be called following a mount.
}

// Type represents a LXD storage pool type.
type Type interface {
	ValidateName(name string) error
	ValidateSource(source string) error
	Validate(config map[string]string) error
}

// Pool represents a LXD storage pool.
type Pool interface {
	Type

	// Pool.
	ID() int64
	Name() string
	Driver() drivers.Driver
	Description() string
	Status() string
	LocalStatus() string
	ToAPI() api.StoragePool

	GetResources() (*api.ResourcesStoragePool, error)
	IsUsed() (bool, error)
	Delete(clientType request.ClientType, op *operations.Operation) error
	Update(clientType request.ClientType, newDesc string, newConfig map[string]string, op *operations.Operation) error

	Create(clientType request.ClientType, op *operations.Operation) error
	Mount() (bool, error)
	Unmount() (bool, error)

	ApplyPatch(name string) error

	GetVolume(volumeType drivers.VolumeType, contentType drivers.ContentType, name string, config map[string]string) drivers.Volume

	// Instances.
	CreateInstance(inst instance.Instance, op *operations.Operation) error
	CreateInstanceFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (func(instance.Instance) error, revert.Hook, error)
	CreateInstanceFromCopy(inst instance.Instance, src instance.Instance, snapshots bool, allowInconsistent bool, op *operations.Operation) error
	CreateInstanceFromImage(inst instance.Instance, fingerprint string, op *operations.Operation) error
	CreateInstanceFromMigration(inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error
	CreateInstanceFromConversion(inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error
	RenameInstance(inst instance.Instance, newName string, op *operations.Operation) error
	DeleteInstance(inst instance.Instance, op *operations.Operation) error
	UpdateInstance(inst instance.Instance, newDesc string, newConfig map[string]string, op *operations.Operation) error
	UpdateInstanceBackupFile(inst instance.Instance, snapshots bool, volBackupConf *backupConfig.Config, version uint32, op *operations.Operation) error
	GenerateInstanceBackupConfig(inst instance.Instance, snapshots bool, volBackupConf *backupConfig.Config, op *operations.Operation) (*backupConfig.Config, error)
	GenerateInstanceCustomVolumeBackupConfig(inst instance.Instance, cache *storageCache, snapshots bool, op *operations.Operation) (*backupConfig.Config, error)
	CheckInstanceBackupFileSnapshots(backupConf *backupConfig.Config, projectName string, op *operations.Operation) ([]*api.InstanceSnapshot, error)
	ImportInstance(inst instance.Instance, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error)
	CleanupInstancePaths(inst instance.Instance, op *operations.Operation) error

	MigrateInstance(inst instance.Instance, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, op *operations.Operation) error
	RefreshInstance(inst instance.Instance, src instance.Instance, srcSnapshots []instance.Instance, allowInconsistent bool, op *operations.Operation) error
	BackupInstance(inst instance.Instance, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, version uint32, op *operations.Operation) error

	GetInstanceUsage(inst instance.Instance) (*VolumeUsage, error)
	SetInstanceQuota(inst instance.Instance, size string, vmStateSize string, op *operations.Operation) error

	MountInstance(inst instance.Instance, op *operations.Operation) (*MountInfo, error)
	UnmountInstance(inst instance.Instance, op *operations.Operation) error

	// Instance snapshots.
	CreateInstanceSnapshot(inst instance.Instance, src instance.Instance, op *operations.Operation) error
	RenameInstanceSnapshot(inst instance.Instance, newName string, op *operations.Operation) error
	DeleteInstanceSnapshot(inst instance.Instance, op *operations.Operation) error
	RestoreInstanceSnapshot(inst instance.Instance, src instance.Instance, op *operations.Operation) error
	MountInstanceSnapshot(inst instance.Instance, op *operations.Operation) (*MountInfo, error)
	UnmountInstanceSnapshot(inst instance.Instance, op *operations.Operation) error
	UpdateInstanceSnapshot(inst instance.Instance, newDesc string, newConfig map[string]string, op *operations.Operation) error

	// Images.
	EnsureImage(fingerprint string, op *operations.Operation, projectName string) error
	DeleteImage(fingerprint string, op *operations.Operation) error
	UpdateImage(fingerprint string, newDesc string, newConfig map[string]string, op *operations.Operation) error

	// Buckets.
	CreateBucket(projectName string, bucket api.StorageBucketsPost, op *operations.Operation) error
	UpdateBucket(projectName string, bucketName string, bucket api.StorageBucketPut, op *operations.Operation) error
	DeleteBucket(projectName string, bucketName string, op *operations.Operation) error
	ImportBucket(projectName string, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error)
	CreateBucketKey(projectName string, bucketName string, key api.StorageBucketKeysPost, op *operations.Operation) (*api.StorageBucketKey, error)
	UpdateBucketKey(projectName string, bucketName string, keyName string, key api.StorageBucketKeyPut, op *operations.Operation) error
	DeleteBucketKey(projectName string, bucketName string, keyName string, op *operations.Operation) error
	ActivateBucket(projectName string, bucketName string, op *operations.Operation) (*miniod.Process, error)
	GetBucketURL(bucketName string) *url.URL

	// Custom volumes.
	CreateCustomVolume(projectName string, volName string, desc string, config map[string]string, contentType drivers.ContentType, op *operations.Operation) error
	CreateCustomVolumeFromCopy(projectName string, srcProjectName string, volName, desc string, config map[string]string, srcPoolName, srcVolName string, snapshots bool, op *operations.Operation) error
	UpdateCustomVolume(projectName string, volName string, newDesc string, newConfig map[string]string, op *operations.Operation) error
	RenameCustomVolume(projectName string, volName string, newVolName string, op *operations.Operation) error
	DeleteCustomVolume(projectName string, volName string, op *operations.Operation) error
	GetCustomVolumeUsage(projectName string, volName string) (*VolumeUsage, error)
	MountCustomVolume(projectName string, volName string, op *operations.Operation) (*MountInfo, error)
	UnmountCustomVolume(projectName string, volName string, op *operations.Operation) (bool, error)
	ImportCustomVolume(projectName string, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error)
	RefreshCustomVolume(projectName string, srcProjectName string, volName, desc string, config map[string]string, srcPoolName, srcVolName string, snapshots bool, op *operations.Operation) error
	UpdateCustomVolumeBackupFiles(projectName string, volName string, snapshots bool, instances []instance.Instance, op *operations.Operation) error
	GenerateCustomVolumeBackupConfig(projectName string, volName string, snapshots bool, op *operations.Operation) (*backupConfig.Config, error)
	CreateCustomVolumeFromISO(projectName string, volName string, srcData io.ReadSeeker, size int64, op *operations.Operation) error
	CreateCustomVolumeFromTarball(projectName string, volName string, srcData *os.File, op *operations.Operation) error

	// Custom volume snapshots.
	CreateCustomVolumeSnapshot(projectName string, volName string, newSnapshotName string, newDescription string, newExpiryDate *time.Time, op *operations.Operation) (*uuid.UUID, error)
	RenameCustomVolumeSnapshot(projectName string, volName string, newSnapshotName string, op *operations.Operation) error
	DeleteCustomVolumeSnapshot(projectName string, volName string, op *operations.Operation) error
	UpdateCustomVolumeSnapshot(projectName string, volName string, newDesc string, newConfig map[string]string, newExpiryDate time.Time, op *operations.Operation) error
	RestoreCustomVolume(projectName string, volName string, snapshotName string, op *operations.Operation) error

	// Custom volume migration.
	MigrationTypes(contentType drivers.ContentType, refresh bool, copySnapshots bool) []migration.Type
	CreateCustomVolumeFromMigration(projectName string, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error
	MigrateCustomVolume(projectName string, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, op *operations.Operation) error

	// Custom volume backups.
	BackupCustomVolume(projectName string, volName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, op *operations.Operation) error
	CreateCustomVolumeFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) error

	// Storage volume recovery.
	ListUnknownVolumes(op *operations.Operation) (map[string][]*backupConfig.Config, error)
}
