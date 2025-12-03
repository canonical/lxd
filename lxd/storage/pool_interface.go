package storage

import (
	"context"
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

	GetResources(ctx context.Context) (*api.ResourcesStoragePool, error)
	IsUsed(ctx context.Context) (bool, error)
	Delete(ctx context.Context, clientType request.ClientType, op *operations.Operation) error
	Update(ctx context.Context, clientType request.ClientType, newDesc string, newConfig map[string]string, op *operations.Operation) error

	Create(ctx context.Context, clientType request.ClientType, op *operations.Operation) error
	Mount(ctx context.Context) (bool, error)
	Unmount(ctx context.Context) (bool, error)

	ApplyPatch(ctx context.Context, name string) error

	GetVolume(volumeType drivers.VolumeType, contentType drivers.ContentType, name string, config map[string]string) drivers.Volume

	// Instances.
	CreateInstance(ctx context.Context, inst instance.Instance, op *operations.Operation) error
	CreateInstanceFromBackup(ctx context.Context, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (func(instance.Instance) error, revert.Hook, error)
	CreateInstanceFromCopy(ctx context.Context, inst instance.Instance, src instance.Instance, snapshots bool, allowInconsistent bool, op *operations.Operation) error
	CreateInstanceFromImage(ctx context.Context, inst instance.Instance, fingerprint string, op *operations.Operation) error
	CreateInstanceFromMigration(ctx context.Context, inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error
	CreateInstanceFromConversion(ctx context.Context, inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error
	RenameInstance(ctx context.Context, inst instance.Instance, newName string, op *operations.Operation) error
	DeleteInstance(ctx context.Context, inst instance.Instance, op *operations.Operation) error
	UpdateInstance(ctx context.Context, inst instance.Instance, newDesc string, newConfig map[string]string, op *operations.Operation) error
	UpdateInstanceBackupFile(ctx context.Context, inst instance.Instance, snapshots bool, volBackupConf *backupConfig.Config, version uint32, op *operations.Operation) error
	GenerateInstanceBackupConfig(ctx context.Context, inst instance.Instance, snapshots bool, volBackupConf *backupConfig.Config, op *operations.Operation) (*backupConfig.Config, error)
	GenerateInstanceCustomVolumeBackupConfig(ctx context.Context, inst instance.Instance, cache *storageCache, snapshots bool, op *operations.Operation) (*backupConfig.Config, error)
	CheckInstanceBackupFileSnapshots(ctx context.Context, backupConf *backupConfig.Config, projectName string, op *operations.Operation) ([]*api.InstanceSnapshot, error)
	ImportInstance(ctx context.Context, inst instance.Instance, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error)
	CleanupInstancePaths(ctx context.Context, inst instance.Instance, op *operations.Operation) error

	MigrateInstance(ctx context.Context, inst instance.Instance, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, op *operations.Operation) error
	RefreshInstance(ctx context.Context, inst instance.Instance, src instance.Instance, srcSnapshots []instance.Instance, allowInconsistent bool, op *operations.Operation) error
	BackupInstance(ctx context.Context, inst instance.Instance, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, version uint32, op *operations.Operation) error

	GetInstanceUsage(ctx context.Context, inst instance.Instance) (*VolumeUsage, error)
	SetInstanceQuota(ctx context.Context, inst instance.Instance, size string, vmStateSize string, op *operations.Operation) error

	MountInstance(ctx context.Context, inst instance.Instance, op *operations.Operation) (*MountInfo, error)
	UnmountInstance(ctx context.Context, inst instance.Instance, op *operations.Operation) error

	// Instance snapshots.
	CreateInstanceSnapshot(ctx context.Context, inst instance.Instance, src instance.Instance, op *operations.Operation) error
	RenameInstanceSnapshot(ctx context.Context, inst instance.Instance, newName string, op *operations.Operation) error
	DeleteInstanceSnapshot(ctx context.Context, inst instance.Instance, op *operations.Operation) error
	RestoreInstanceSnapshot(ctx context.Context, inst instance.Instance, src instance.Instance, op *operations.Operation) error
	MountInstanceSnapshot(ctx context.Context, inst instance.Instance, op *operations.Operation) (*MountInfo, error)
	UnmountInstanceSnapshot(ctx context.Context, inst instance.Instance, op *operations.Operation) error
	UpdateInstanceSnapshot(ctx context.Context, inst instance.Instance, newDesc string, newConfig map[string]string, op *operations.Operation) error

	// Images.
	EnsureImage(ctx context.Context, fingerprint string, op *operations.Operation, projectName string) error
	DeleteImage(ctx context.Context, fingerprint string, op *operations.Operation) error
	UpdateImage(ctx context.Context, fingerprint string, newDesc string, newConfig map[string]string, op *operations.Operation) error

	// Buckets.
	CreateBucket(ctx context.Context, projectName string, bucket api.StorageBucketsPost, op *operations.Operation) error
	UpdateBucket(ctx context.Context, projectName string, bucketName string, bucket api.StorageBucketPut, op *operations.Operation) error
	DeleteBucket(ctx context.Context, projectName string, bucketName string, op *operations.Operation) error
	ImportBucket(ctx context.Context, projectName string, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error)
	CreateBucketKey(ctx context.Context, projectName string, bucketName string, key api.StorageBucketKeysPost, op *operations.Operation) (*api.StorageBucketKey, error)
	UpdateBucketKey(ctx context.Context, projectName string, bucketName string, keyName string, key api.StorageBucketKeyPut, op *operations.Operation) error
	DeleteBucketKey(ctx context.Context, projectName string, bucketName string, keyName string, op *operations.Operation) error
	ActivateBucket(ctx context.Context, projectName string, bucketName string, op *operations.Operation) (*miniod.Process, error)
	GetBucketURL(ctx context.Context, bucketName string) *url.URL

	// Custom volumes.
	CreateCustomVolume(ctx context.Context, projectName string, volName string, desc string, config map[string]string, contentType drivers.ContentType, op *operations.Operation) error
	CreateCustomVolumeFromCopy(ctx context.Context, projectName string, srcProjectName string, volName, desc string, config map[string]string, srcPoolName, srcVolName string, snapshots bool, op *operations.Operation) error
	UpdateCustomVolume(ctx context.Context, projectName string, volName string, newDesc string, newConfig map[string]string, op *operations.Operation) error
	RenameCustomVolume(ctx context.Context, projectName string, volName string, newVolName string, op *operations.Operation) error
	DeleteCustomVolume(ctx context.Context, projectName string, volName string, op *operations.Operation) error
	GetCustomVolumeUsage(ctx context.Context, projectName string, volName string) (*VolumeUsage, error)
	MountCustomVolume(ctx context.Context, projectName string, volName string, op *operations.Operation) (*MountInfo, error)
	UnmountCustomVolume(ctx context.Context, projectName string, volName string, op *operations.Operation) (bool, error)
	ImportCustomVolume(ctx context.Context, projectName string, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error)
	RefreshCustomVolume(ctx context.Context, projectName string, srcProjectName string, volName, desc string, config map[string]string, srcPoolName, srcVolName string, snapshots bool, op *operations.Operation) error
	UpdateCustomVolumeBackupFiles(ctx context.Context, projectName string, volName string, snapshots bool, instances []instance.Instance, op *operations.Operation) error
	GenerateCustomVolumeBackupConfig(ctx context.Context, projectName string, volName string, snapshots bool, op *operations.Operation) (*backupConfig.Config, error)
	CreateCustomVolumeFromISO(ctx context.Context, projectName string, volName string, srcData io.ReadSeeker, size int64, op *operations.Operation) error
	CreateCustomVolumeFromTarball(ctx context.Context, projectName string, volName string, srcData *os.File, op *operations.Operation) error

	// Custom volume snapshots.
	CreateCustomVolumeSnapshot(ctx context.Context, projectName string, volName string, newSnapshotName string, newDescription string, newExpiryDate *time.Time, op *operations.Operation) (*uuid.UUID, error)
	RenameCustomVolumeSnapshot(ctx context.Context, projectName string, volName string, newSnapshotName string, op *operations.Operation) error
	DeleteCustomVolumeSnapshot(ctx context.Context, projectName string, volName string, op *operations.Operation) error
	UpdateCustomVolumeSnapshot(ctx context.Context, projectName string, volName string, newDesc string, newConfig map[string]string, newExpiryDate time.Time, op *operations.Operation) error
	RestoreCustomVolume(ctx context.Context, projectName string, volName string, snapshotName string, op *operations.Operation) error

	// Custom volume migration.
	MigrationTypes(contentType drivers.ContentType, refresh bool, copySnapshots bool) []migration.Type
	CreateCustomVolumeFromMigration(ctx context.Context, projectName string, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error
	MigrateCustomVolume(ctx context.Context, projectName string, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, op *operations.Operation) error

	// Custom volume backups.
	BackupCustomVolume(ctx context.Context, projectName string, volName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, op *operations.Operation) error
	CreateCustomVolumeFromBackup(ctx context.Context, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) error

	// Storage volume recovery.
	ListUnknownVolumes(ctx context.Context, op *operations.Operation) (map[string][]*backupConfig.Config, error)
}
