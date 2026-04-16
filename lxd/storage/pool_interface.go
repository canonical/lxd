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
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/ioprogress"
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

	GetResources() (*api.ResourcesStoragePool, error)
	IsUsed() (bool, error)
	Delete(clientType request.ClientType, progressReporter ioprogress.ProgressReporter) error
	Update(clientType request.ClientType, newDesc string, newConfig map[string]string, progressReporter ioprogress.ProgressReporter) error

	Create(clientType request.ClientType, progressReporter ioprogress.ProgressReporter) error
	Mount() (bool, error)
	Unmount() (bool, error)

	ApplyPatch(name string) error

	GetVolume(volumeType drivers.VolumeType, contentType drivers.ContentType, name string, config map[string]string) drivers.Volume

	// Instances.
	CreateInstance(inst instance.Instance, progressReporter ioprogress.ProgressReporter) error
	CreateInstanceFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, progressReporter ioprogress.ProgressReporter) (func(instance.Instance) error, revert.Hook, error)
	CreateInstanceFromCopy(ctx context.Context, inst instance.Instance, src instance.Instance, snapshots bool, allowInconsistent bool, progressReporter ioprogress.ProgressReporter) error
	CreateInstanceFromImage(ctx context.Context, inst instance.Instance, fingerprint string, progressReporter ioprogress.ProgressReporter) error
	CreateInstanceFromMigration(ctx context.Context, inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, progressReporter ioprogress.ProgressReporter) error
	CreateInstanceFromConversion(inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, progressReporter ioprogress.ProgressReporter) error
	RenameInstance(inst instance.Instance, newName string, progressReporter ioprogress.ProgressReporter) error
	DeleteInstance(inst instance.Instance, progressReporter ioprogress.ProgressReporter) error
	UpdateInstance(ctx context.Context, inst instance.Instance, newDesc string, newConfig map[string]string, progressReporter ioprogress.ProgressReporter) error
	UpdateInstanceBackupFile(inst instance.Instance, snapshots bool, volBackupConf *backupConfig.Config, version uint32, progressReporter ioprogress.ProgressReporter) error
	GenerateInstanceBackupConfig(inst instance.Instance, snapshots bool, volBackupConf *backupConfig.Config, progressReporter ioprogress.ProgressReporter) (*backupConfig.Config, error)
	GenerateInstanceCustomVolumeBackupConfig(inst instance.Instance, cache *storageCache, snapshots bool, progressReporter ioprogress.ProgressReporter) (*backupConfig.Config, error)
	CheckInstanceBackupFileSnapshots(backupConf *backupConfig.Config, projectName string, progressReporter ioprogress.ProgressReporter) ([]*api.InstanceSnapshot, error)
	ImportInstance(inst instance.Instance, poolVol *backupConfig.Config, progressReporter ioprogress.ProgressReporter) (revert.Hook, error)
	CleanupInstancePaths(inst instance.Instance, progressReporter ioprogress.ProgressReporter) error

	MigrateInstance(ctx context.Context, inst instance.Instance, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, progressReporter ioprogress.ProgressReporter) error
	RefreshInstance(ctx context.Context, inst instance.Instance, src instance.Instance, srcSnapshots []instance.Instance, allowInconsistent bool, progressReporter ioprogress.ProgressReporter) error
	BackupInstance(inst instance.Instance, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, version uint32, progressReporter ioprogress.ProgressReporter) error

	GetInstanceUsage(inst instance.Instance) (*VolumeUsage, error)
	SetInstanceQuota(inst instance.Instance, size string, vmStateSize string, progressReporter ioprogress.ProgressReporter) error

	MountInstance(inst instance.Instance, progressReporter ioprogress.ProgressReporter) (*MountInfo, error)
	UnmountInstance(inst instance.Instance, progressReporter ioprogress.ProgressReporter) error

	// Instance snapshots.
	CreateInstanceSnapshot(inst instance.Instance, src instance.Instance, progressReporter ioprogress.ProgressReporter) error
	RenameInstanceSnapshot(inst instance.Instance, newName string, progressReporter ioprogress.ProgressReporter) error
	DeleteInstanceSnapshot(inst instance.Instance, progressReporter ioprogress.ProgressReporter) error
	RestoreInstanceSnapshot(ctx context.Context, inst instance.Instance, src instance.Instance, progressReporter ioprogress.ProgressReporter) error
	MountInstanceSnapshot(inst instance.Instance, progressReporter ioprogress.ProgressReporter) (*MountInfo, error)
	UnmountInstanceSnapshot(inst instance.Instance, progressReporter ioprogress.ProgressReporter) error
	UpdateInstanceSnapshot(ctx context.Context, inst instance.Instance, newDesc string, newConfig map[string]string, progressReporter ioprogress.ProgressReporter) error

	// Images.
	EnsureImage(ctx context.Context, fingerprint string, projectName string, progressReporter ioprogress.ProgressReporter) error
	DeleteImage(ctx context.Context, fingerprint string, progressReporter ioprogress.ProgressReporter) error
	UpdateImage(ctx context.Context, fingerprint string, newDesc string, newConfig map[string]string, progressReporter ioprogress.ProgressReporter) error

	// Buckets.
	CreateBucket(projectName string, bucket api.StorageBucketsPost) error
	UpdateBucket(projectName string, bucketName string, bucket api.StorageBucketPut) error
	DeleteBucket(projectName string, bucketName string) error
	CreateBucketKey(projectName string, bucketName string, key api.StorageBucketKeysPost) (*api.StorageBucketKey, error)
	UpdateBucketKey(projectName string, bucketName string, keyName string, key api.StorageBucketKeyPut) error
	DeleteBucketKey(projectName string, bucketName string, keyName string) error
	GetBucketURL(bucketName string) *url.URL

	// Custom volumes.
	CreateCustomVolume(ctx context.Context, projectName string, volName string, desc string, config map[string]string, contentType drivers.ContentType, progressReporter ioprogress.ProgressReporter) error
	CreateCustomVolumeFromCopy(ctx context.Context, projectName, srcProjectName, volName, desc string, config map[string]string, srcPoolName, srcVolName string, snapshots bool, progressReporter ioprogress.ProgressReporter) error
	UpdateCustomVolume(ctx context.Context, projectName string, volName string, newDesc string, newConfig map[string]string, progressReporter ioprogress.ProgressReporter) error
	RenameCustomVolume(ctx context.Context, projectName string, volName string, newVolName string, progressReporter ioprogress.ProgressReporter) error
	DeleteCustomVolume(ctx context.Context, projectName string, volName string, progressReporter ioprogress.ProgressReporter) error
	GetCustomVolumeUsage(projectName string, volName string) (*VolumeUsage, error)
	MountCustomVolume(projectName string, volName string, progressReporter ioprogress.ProgressReporter) (*MountInfo, error)
	UnmountCustomVolume(projectName string, volName string, progressReporter ioprogress.ProgressReporter) (bool, error)
	ImportCustomVolume(projectName string, poolVol *backupConfig.Config, progressReporter ioprogress.ProgressReporter) (revert.Hook, error)
	RefreshCustomVolume(ctx context.Context, projectName, srcProjectName, volName, desc string, config map[string]string, srcPoolName, srcVolName string, snapshots bool, progressReporter ioprogress.ProgressReporter) error
	UpdateCustomVolumeBackupFiles(projectName string, volName string, snapshots bool, instances []instance.Instance, progressReporter ioprogress.ProgressReporter) error
	GenerateCustomVolumeBackupConfig(projectName string, volName string, snapshots bool, progressReporter ioprogress.ProgressReporter) (*backupConfig.Config, error)
	CreateCustomVolumeFromISO(ctx context.Context, projectName string, volName string, srcData io.ReadSeeker, size int64, progressReporter ioprogress.ProgressReporter) error
	CreateCustomVolumeFromTarball(ctx context.Context, projectName string, volName string, srcData *os.File, progressReporter ioprogress.ProgressReporter) error

	// Custom volume snapshots.
	CreateCustomVolumeSnapshot(ctx context.Context, projectName string, volName string, newSnapshotName string, newDescription string, newExpiryDate *time.Time, progressReporter ioprogress.ProgressReporter) (*uuid.UUID, error)
	RenameCustomVolumeSnapshot(ctx context.Context, projectName string, volName string, newSnapshotName string, progressReporter ioprogress.ProgressReporter) error
	DeleteCustomVolumeSnapshot(ctx context.Context, projectName string, volName string, progressReporter ioprogress.ProgressReporter) error
	UpdateCustomVolumeSnapshot(ctx context.Context, projectName string, volName string, newDesc string, newConfig map[string]string, newExpiryDate time.Time, progressReporter ioprogress.ProgressReporter) error
	RestoreCustomVolume(ctx context.Context, projectName string, volName string, snapshotName string, progressReporter ioprogress.ProgressReporter) error

	// Custom volume migration.
	MigrationTypes(contentType drivers.ContentType, refresh bool, copySnapshots bool) []migration.Type
	CreateCustomVolumeFromMigration(ctx context.Context, projectName string, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, progressReporter ioprogress.ProgressReporter) error
	MigrateCustomVolume(projectName string, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, progressReporter ioprogress.ProgressReporter) error

	// Custom volume backups.
	BackupCustomVolume(projectName string, volName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, progressReporter ioprogress.ProgressReporter) error
	CreateCustomVolumeFromBackup(ctx context.Context, srcBackup backup.Info, srcData io.ReadSeeker, progressReporter ioprogress.ProgressReporter) error

	// Storage volume recovery.
	ListUnknownVolumes(progressReporter ioprogress.ProgressReporter) (map[string][]*backupConfig.Config, error)
}
