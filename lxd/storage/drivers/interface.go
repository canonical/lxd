package drivers

import (
	"context"
	"io"
	"net/url"

	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

// driver is the extended internal interface.
type driver interface {
	Driver

	init(state *state.State, name string, config map[string]string, logger logger.Logger, volIDFunc func(ctx context.Context, volType VolumeType, volName string) (int64, error), commonRules *Validators)
	load(ctx context.Context) error
	isRemote() bool
	defaultVMBlockFilesystemSize() string
}

// Driver represents a low-level storage driver.
type Driver interface {
	// Internal.
	Info() Info
	HasVolume(ctx context.Context, vol Volume) (bool, error)
	roundVolumeBlockSizeBytes(ctx context.Context, vol Volume, sizeBytes int64) int64
	isBlockBacked(vol Volume) bool

	// Export struct details.
	Name() string
	SourceIdentifier() (string, error)
	Config() map[string]string
	Logger() logger.Logger

	// Pool.
	FillConfig(ctx context.Context) error
	Create(ctx context.Context) error
	Delete(ctx context.Context, op *operations.Operation) error
	// Mount mounts a storage pool if needed, returns true if we caused a new mount, false if already mounted.
	Mount(ctx context.Context) (bool, error)

	// Unmount unmounts a storage pool if needed, returns true if unmounted, false if was not mounted.
	Unmount(ctx context.Context) (bool, error)
	GetResources(ctx context.Context) (*api.ResourcesStoragePool, error)
	Validate(config map[string]string) error
	ValidateSource() error
	Update(ctx context.Context, changedConfig map[string]string) error
	ApplyPatch(ctx context.Context, name string) error

	// Buckets.
	ValidateBucket(bucket Volume) error
	GetBucketURL(bucketName string) *url.URL
	CreateBucket(ctx context.Context, bucket Volume, op *operations.Operation) error
	DeleteBucket(ctx context.Context, bucket Volume, op *operations.Operation) error
	UpdateBucket(ctx context.Context, bucket Volume, changedConfig map[string]string) error
	ValidateBucketKey(keyName string, creds S3Credentials, roleName string) error
	CreateBucketKey(ctx context.Context, bucket Volume, keyName string, creds S3Credentials, roleName string, op *operations.Operation) (*S3Credentials, error)
	UpdateBucketKey(ctx context.Context, bucket Volume, keyName string, creds S3Credentials, roleName string, op *operations.Operation) (*S3Credentials, error)
	DeleteBucketKey(ctx context.Context, bucket Volume, keyName string, op *operations.Operation) error

	// Volumes.
	FillVolumeConfig(vol Volume) error
	ValidateVolume(ctx context.Context, vol Volume, removeUnknownKeys bool) error
	CreateVolume(ctx context.Context, vol Volume, filler *VolumeFiller, op *operations.Operation) error
	CreateVolumeFromCopy(ctx context.Context, vol VolumeCopy, srcVol VolumeCopy, allowInconsistent bool, op *operations.Operation) error
	RefreshVolume(ctx context.Context, vol VolumeCopy, srcVol VolumeCopy, refreshSnapshots []string, allowInconsistent bool, op *operations.Operation) error
	DeleteVolume(ctx context.Context, vol Volume, op *operations.Operation) error
	RenameVolume(ctx context.Context, vol Volume, newName string, op *operations.Operation) error
	UpdateVolume(ctx context.Context, vol Volume, changedConfig map[string]string) error
	GetVolumeUsage(ctx context.Context, vol Volume) (int64, error)
	SetVolumeQuota(ctx context.Context, vol Volume, size string, allowUnsafeResize bool, op *operations.Operation) error
	GetVolumeDiskPath(ctx context.Context, vol Volume) (string, error)
	ListVolumes(ctx context.Context) ([]Volume, error)

	// MountVolume mounts a storage volume (if not mounted) and increments reference counter.
	MountVolume(ctx context.Context, vol Volume, op *operations.Operation) error

	// MountVolumeSnapshot mounts a storage volume snapshot as readonly.
	MountVolumeSnapshot(ctx context.Context, snapVol Volume, op *operations.Operation) error

	// CanDelegateVolume checks whether the volume can be delegated.
	CanDelegateVolume(ctx context.Context, vol Volume) bool

	// DelegateVolume allows for the volume to be managed by the instance.
	DelegateVolume(ctx context.Context, vol Volume, pid int) error

	// UnmountVolume unmounts a storage volume, returns true if unmounted, false if was not
	// mounted.
	UnmountVolume(ctx context.Context, vol Volume, keepBlockDev bool, op *operations.Operation) (bool, error)

	// UnmountVolume unmounts a storage volume snapshot, returns true if unmounted, false if was
	// not mounted.
	UnmountVolumeSnapshot(ctx context.Context, snapVol Volume, op *operations.Operation) (bool, error)

	CreateVolumeSnapshot(ctx context.Context, snapVol Volume, op *operations.Operation) error
	DeleteVolumeSnapshot(ctx context.Context, snapVol Volume, op *operations.Operation) error
	RenameVolumeSnapshot(ctx context.Context, snapVol Volume, newSnapshotName string, op *operations.Operation) error
	VolumeSnapshots(ctx context.Context, vol Volume, op *operations.Operation) ([]string, error)
	CheckVolumeSnapshots(ctx context.Context, vol Volume, snapVols []Volume, op *operations.Operation) error
	RestoreVolume(ctx context.Context, vol Volume, snapVol Volume, op *operations.Operation) error

	// Migration.
	MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool) []migration.Type
	MigrateVolume(ctx context.Context, vol VolumeCopy, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error
	CreateVolumeFromMigration(ctx context.Context, vol VolumeCopy, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error

	// Backup.
	BackupVolume(ctx context.Context, vol VolumeCopy, projectName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, op *operations.Operation) error
	CreateVolumeFromBackup(ctx context.Context, vol VolumeCopy, srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (VolumePostHook, revert.Hook, error)
}
