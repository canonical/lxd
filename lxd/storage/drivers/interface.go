package drivers

import (
	"io"
	"net/url"

	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

// driver is the extended internal interface.
type driver interface {
	Driver

	init(state *state.State, name string, config map[string]string, logger logger.Logger, volIDFunc func(volType VolumeType, volName string) (int64, error), commonRules *Validators)
	load() error
	isRemote() bool
	defaultVMBlockFilesystemSize() string
}

// Driver represents a low-level storage driver.
type Driver interface {
	// Internal.
	Info() Info
	HasVolume(vol Volume) (bool, error)
	roundVolumeBlockSizeBytes(vol Volume, sizeBytes int64) int64
	isBlockBacked(vol Volume) bool
	ImageVolumeConfigMatch(vol1 Volume, vol2 Volume) bool

	// Export struct details.
	Name() string
	SourceIdentifier() (string, error)
	Config() map[string]string
	Logger() logger.Logger

	// Pool.
	FillConfig() error
	Create() error
	Delete(progressReporter ioprogress.ProgressReporter) error
	// Mount mounts a storage pool if needed, returns true if we caused a new mount, false if already mounted.
	Mount() (bool, error)

	// Unmount unmounts a storage pool if needed, returns true if unmounted, false if was not mounted.
	Unmount() (bool, error)
	GetResources() (*api.ResourcesStoragePool, error)
	Validate(config map[string]string) error
	ValidateSource() error
	Update(changedConfig map[string]string) error
	ApplyPatch(name string) error

	// Buckets.
	ValidateBucket(bucket Volume) error
	GetBucketURL(bucketName string) *url.URL
	CreateBucket(bucket Volume) error
	DeleteBucket(bucket Volume) error
	UpdateBucket(bucket Volume, changedConfig map[string]string) error
	ValidateBucketKey(keyName string, creds S3Credentials, roleName string) error
	CreateBucketKey(bucket Volume, keyName string, creds S3Credentials, roleName string) (*S3Credentials, error)
	UpdateBucketKey(bucket Volume, keyName string, creds S3Credentials, roleName string) (*S3Credentials, error)
	DeleteBucketKey(bucket Volume, keyName string) error

	// Volumes.
	FillVolumeConfig(vol Volume) error
	ValidateVolume(vol Volume, removeUnknownKeys bool) error
	CreateVolume(vol Volume, filler *VolumeFiller, progressReporter ioprogress.ProgressReporter) error
	CreateVolumeFromCopy(vol VolumeCopy, srcVol VolumeCopy, allowInconsistent bool, progressReporter ioprogress.ProgressReporter) error
	CreateVolumeFromImage(vol Volume, imgVol *Volume, filler *VolumeFiller, progressReporter ioprogress.ProgressReporter) error
	RefreshVolume(vol VolumeCopy, srcVol VolumeCopy, refreshSnapshots []string, allowInconsistent bool, progressReporter ioprogress.ProgressReporter) error
	DeleteVolume(vol Volume, progressReporter ioprogress.ProgressReporter) error
	RenameVolume(vol Volume, newName string, progressReporter ioprogress.ProgressReporter) error
	UpdateVolume(vol Volume, changedConfig map[string]string) error
	GetVolumeUsage(vol Volume) (int64, error)
	SetVolumeQuota(vol Volume, size string, allowUnsafeResize bool, progressReporter ioprogress.ProgressReporter) error
	GetVolumeDiskPath(vol Volume) (string, error)
	ListVolumes() ([]Volume, error)

	// MountVolume mounts a storage volume (if not mounted) and increments reference counter.
	MountVolume(vol Volume, progressReporter ioprogress.ProgressReporter) error

	// MountVolumeSnapshot mounts a storage volume snapshot as readonly.
	MountVolumeSnapshot(snapVol Volume, progressReporter ioprogress.ProgressReporter) error

	// CanDelegateVolume checks whether the volume can be delegated.
	CanDelegateVolume(vol Volume) bool

	// DelegateVolume allows for the volume to be managed by the instance.
	DelegateVolume(vol Volume, pid int) error

	// UnmountVolume unmounts a storage volume, returns true if unmounted, false if was not
	// mounted.
	UnmountVolume(vol Volume, keepBlockDev bool, progressReporter ioprogress.ProgressReporter) (bool, error)

	// UnmountVolume unmounts a storage volume snapshot, returns true if unmounted, false if was
	// not mounted.
	UnmountVolumeSnapshot(snapVol Volume, progressReporter ioprogress.ProgressReporter) (bool, error)

	CreateVolumeSnapshot(snapVol Volume, progressReporter ioprogress.ProgressReporter) error
	DeleteVolumeSnapshot(snapVol Volume, progressReporter ioprogress.ProgressReporter) error
	RenameVolumeSnapshot(snapVol Volume, newSnapshotName string, progressReporter ioprogress.ProgressReporter) error
	VolumeSnapshots(vol Volume) ([]string, error)
	CheckVolumeSnapshots(vol Volume, snapVols []Volume) error
	RestoreVolume(vol Volume, snapVol Volume, progressReporter ioprogress.ProgressReporter) error

	// Migration.
	MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool) []migration.Type
	MigrateVolume(vol VolumeCopy, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, progressReporter ioprogress.ProgressReporter) error
	CreateVolumeFromMigration(vol VolumeCopy, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, progressReporter ioprogress.ProgressReporter) error

	// Backup.
	BackupVolume(vol VolumeCopy, projectName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots []string, progressReporter ioprogress.ProgressReporter) error
	CreateVolumeFromBackup(vol VolumeCopy, srcBackup backup.Info, srcData io.ReadSeeker, progressReporter ioprogress.ProgressReporter) (VolumePostHook, revert.Hook, error)
}
