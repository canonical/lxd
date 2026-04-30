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
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

type mockBackend struct {
	name   string
	state  *state.State
	logger logger.Logger
	driver drivers.Driver
}

// ID ...
func (b *mockBackend) ID() int64 {
	return 1 //  The tests expect the storage pool ID to be 1.
}

// Name ...
func (b *mockBackend) Name() string {
	return b.name
}

// Description ...
func (b *mockBackend) Description() string {
	return ""
}

// ValidateName ...
func (b *mockBackend) ValidateName(value string) error {
	return nil
}

// Validate ...
func (b *mockBackend) Validate(config map[string]string) error {
	return nil
}

// Status ...
func (b *mockBackend) Status() string {
	return api.NetworkStatusUnknown
}

// LocalStatus ...
func (b *mockBackend) LocalStatus() string {
	return api.NetworkStatusUnknown
}

// ToAPI ...
func (b *mockBackend) ToAPI() api.StoragePool {
	return api.StoragePool{}
}

// Driver ...
func (b *mockBackend) Driver() drivers.Driver {
	return b.driver
}

// MigrationTypes ...
func (b *mockBackend) MigrationTypes(contentType drivers.ContentType, refresh bool, copySnapshots bool) []migration.Type {
	return []migration.Type{
		{
			FSType:   FallbackMigrationType(contentType),
			Features: []string{"xattrs", "delete", "compress", "bidirectional"},
		},
	}
}

// GetResources ...
func (b *mockBackend) GetResources() (*api.ResourcesStoragePool, error) {
	return nil, nil
}

// IsUsed ...
func (b *mockBackend) IsUsed() (bool, error) {
	return false, nil
}

// Delete ...
func (b *mockBackend) Delete(clientType request.ClientType, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// Update ...
func (b *mockBackend) Update(clientType request.ClientType, newDescription string, newConfig map[string]string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// Create ...
func (b *mockBackend) Create(clientType request.ClientType, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// Mount ...
func (b *mockBackend) Mount() (bool, error) {
	return true, nil
}

// Unmount ...
func (b *mockBackend) Unmount() (bool, error) {
	return true, nil
}

// ApplyPatch ...
func (b *mockBackend) ApplyPatch(name string) error {
	return nil
}

// GetVolume ...
func (b *mockBackend) GetVolume(volType drivers.VolumeType, contentType drivers.ContentType, volName string, volConfig map[string]string) drivers.Volume {
	return drivers.Volume{}
}

// CreateInstance ...
func (b *mockBackend) CreateInstance(inst instance.Instance, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// CreateInstanceFromBackup ...
func (b *mockBackend) CreateInstanceFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, progressReporter ioprogress.ProgressReporter) (func(instance.Instance) error, revert.Hook, error) {
	return nil, nil, nil
}

// CreateInstanceFromCopy ...
func (b *mockBackend) CreateInstanceFromCopy(ctx context.Context, inst instance.Instance, src instance.Instance, snapshots bool, allowInconsistent bool, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// CreateInstanceFromImage ...
func (b *mockBackend) CreateInstanceFromImage(ctx context.Context, inst instance.Instance, fingerprint string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// CreateInstanceFromMigration ...
func (b *mockBackend) CreateInstanceFromMigration(ctx context.Context, inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// CreateInstanceFromConversion ...
func (b *mockBackend) CreateInstanceFromConversion(inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// RenameInstance ...
func (b *mockBackend) RenameInstance(inst instance.Instance, newName string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// DeleteInstance ...
func (b *mockBackend) DeleteInstance(inst instance.Instance, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// UpdateInstance ...
func (b *mockBackend) UpdateInstance(ctx context.Context, inst instance.Instance, newDesc string, newConfig map[string]string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// GenerateCustomVolumeBackupConfig ...
func (b *mockBackend) GenerateCustomVolumeBackupConfig(projectName string, volName string, snapshots bool, progressReporter ioprogress.ProgressReporter) (*backupConfig.Config, error) {
	return nil, nil
}

// GenerateInstanceBackupConfig ...
func (b *mockBackend) GenerateInstanceBackupConfig(inst instance.Instance, snapshots bool, volBackupConf *backupConfig.Config, progressReporter ioprogress.ProgressReporter) (*backupConfig.Config, error) {
	return nil, nil
}

// GenerateInstanceCustomVolumeBackupConfig ...
func (b *mockBackend) GenerateInstanceCustomVolumeBackupConfig(inst instance.Instance, cache *storageCache, snapshots bool, progressReporter ioprogress.ProgressReporter) (*backupConfig.Config, error) {
	return nil, nil
}

// UpdateInstanceBackupFile ...
func (b *mockBackend) UpdateInstanceBackupFile(inst instance.Instance, snapshot bool, volBackupConf *backupConfig.Config, version uint32, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// UpdateCustomVolumeBackupFiles ...
func (b *mockBackend) UpdateCustomVolumeBackupFiles(projectName string, volName string, snapshots bool, instances []instance.Instance, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// CheckInstanceBackupFileSnapshots checks the snapshots in storage against the given backup config.
func (b *mockBackend) CheckInstanceBackupFileSnapshots(backupConf *backupConfig.Config, projectName string, progressReporter ioprogress.ProgressReporter) ([]*api.InstanceSnapshot, error) {
	return nil, nil
}

// ListUnknownVolumes ...
func (b *mockBackend) ListUnknownVolumes(progressReporter ioprogress.ProgressReporter) (map[string][]*backupConfig.Config, error) {
	return nil, nil
}

// ImportInstance ...
func (b *mockBackend) ImportInstance(inst instance.Instance, poolVol *backupConfig.Config, progressReporter ioprogress.ProgressReporter) (revert.Hook, error) {
	return nil, nil
}

// MigrateInstance ...
func (b *mockBackend) MigrateInstance(ctx context.Context, inst instance.Instance, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// CleanupInstancePaths ...
func (b *mockBackend) CleanupInstancePaths(inst instance.Instance, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// RefreshCustomVolume ...
func (b *mockBackend) RefreshCustomVolume(ctx context.Context, projectName, srcProjectName, volName, desc string, config map[string]string, srcPoolName, srcVolName string, snapshots bool, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// RefreshInstance ...
func (b *mockBackend) RefreshInstance(ctx context.Context, inst instance.Instance, src instance.Instance, srcSnapshots []instance.Instance, allowInconsistent bool, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// BackupInstance ...
func (b *mockBackend) BackupInstance(inst instance.Instance, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, version uint32, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// GetInstanceUsage ...
func (b *mockBackend) GetInstanceUsage(inst instance.Instance) (*VolumeUsage, error) {
	return nil, nil
}

// SetInstanceQuota ...
func (b *mockBackend) SetInstanceQuota(inst instance.Instance, size string, vmStateSize string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// MountInstance ...
func (b *mockBackend) MountInstance(inst instance.Instance, progressReporter ioprogress.ProgressReporter) (*MountInfo, error) {
	return &MountInfo{}, nil
}

// UnmountInstance ...
func (b *mockBackend) UnmountInstance(inst instance.Instance, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// CreateInstanceSnapshot ...
func (b *mockBackend) CreateInstanceSnapshot(i instance.Instance, src instance.Instance, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// RenameInstanceSnapshot ...
func (b *mockBackend) RenameInstanceSnapshot(inst instance.Instance, newName string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// DeleteInstanceSnapshot ...
func (b *mockBackend) DeleteInstanceSnapshot(inst instance.Instance, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// RestoreInstanceSnapshot ...
func (b *mockBackend) RestoreInstanceSnapshot(ctx context.Context, inst instance.Instance, src instance.Instance, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// MountInstanceSnapshot ...
func (b *mockBackend) MountInstanceSnapshot(inst instance.Instance, progressReporter ioprogress.ProgressReporter) (*MountInfo, error) {
	return &MountInfo{}, nil
}

// UnmountInstanceSnapshot ...
func (b *mockBackend) UnmountInstanceSnapshot(inst instance.Instance, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// UpdateInstanceSnapshot ...
func (b *mockBackend) UpdateInstanceSnapshot(ctx context.Context, inst instance.Instance, newDesc string, newConfig map[string]string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// EnsureImage ...
func (b *mockBackend) EnsureImage(ctx context.Context, fingerprint string, projectName string, inst instance.Instance, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// DeleteImage ...
func (b *mockBackend) DeleteImage(ctx context.Context, fingerprint string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// UpdateImage ...
func (b *mockBackend) UpdateImage(ctx context.Context, fingerprint string, newDesc string, newConfig map[string]string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// CreateBucket ...
func (b *mockBackend) CreateBucket(projectName string, bucket api.StorageBucketsPost) error {
	return nil
}

// UpdateBucket ...
func (b *mockBackend) UpdateBucket(projectName string, bucketName string, bucket api.StorageBucketPut) error {
	return nil
}

// DeleteBucket ...
func (b *mockBackend) DeleteBucket(projectName string, bucketName string) error {
	return nil
}

// CreateBucketKey ...
func (b *mockBackend) CreateBucketKey(projectName string, bucketName string, key api.StorageBucketKeysPost) (*api.StorageBucketKey, error) {
	return nil, nil
}

// UpdateBucketKey ...
func (b *mockBackend) UpdateBucketKey(projectName string, bucketName string, keyName string, key api.StorageBucketKeyPut) error {
	return nil
}

// DeleteBucketKey ...
func (b *mockBackend) DeleteBucketKey(projectName string, bucketName string, keyName string) error {
	return nil
}

// GetBucketURL ...
func (b *mockBackend) GetBucketURL(bucketName string) *url.URL {
	return nil
}

// CreateCustomVolume ...
func (b *mockBackend) CreateCustomVolume(ctx context.Context, projectName string, volName string, desc string, config map[string]string, contentType drivers.ContentType, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// CreateCustomVolumeFromCopy ...
func (b *mockBackend) CreateCustomVolumeFromCopy(ctx context.Context, projectName, srcProjectName, volName, desc string, config map[string]string, srcPoolName, srcVolName string, snapshots bool, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// RenameCustomVolume ...
func (b *mockBackend) RenameCustomVolume(ctx context.Context, projectName string, volName string, newVolName string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// UpdateCustomVolume ...
func (b *mockBackend) UpdateCustomVolume(ctx context.Context, projectName string, volName string, newDesc string, newConfig map[string]string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// DeleteCustomVolume ...
func (b *mockBackend) DeleteCustomVolume(ctx context.Context, projectName string, volName string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// MigrateCustomVolume ...
func (b *mockBackend) MigrateCustomVolume(projectName string, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// CreateCustomVolumeFromMigration ...
func (b *mockBackend) CreateCustomVolumeFromMigration(ctx context.Context, projectName string, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// GetCustomVolumeUsage ...
func (b *mockBackend) GetCustomVolumeUsage(projectName string, volName string) (*VolumeUsage, error) {
	return nil, nil
}

// MountCustomVolume ...
func (b *mockBackend) MountCustomVolume(projectName string, volName string, progressReporter ioprogress.ProgressReporter) (*MountInfo, error) {
	return nil, nil
}

// UnmountCustomVolume ...
func (b *mockBackend) UnmountCustomVolume(projectName string, volName string, progressReporter ioprogress.ProgressReporter) (bool, error) {
	return true, nil
}

// ImportCustomVolume ...
func (b *mockBackend) ImportCustomVolume(projectName string, poolVol *backupConfig.Config, progressReporter ioprogress.ProgressReporter) (revert.Hook, error) {
	return nil, nil
}

// CreateCustomVolumeSnapshot ...
func (b *mockBackend) CreateCustomVolumeSnapshot(ctx context.Context, projectName string, volName string, newSnapshotName string, newDescription string, newExpiryDate *time.Time, progressReporter ioprogress.ProgressReporter) (*uuid.UUID, error) {
	return nil, nil
}

// RenameCustomVolumeSnapshot ...
func (b *mockBackend) RenameCustomVolumeSnapshot(ctx context.Context, projectName string, volName string, newSnapshotName string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// DeleteCustomVolumeSnapshot ...
func (b *mockBackend) DeleteCustomVolumeSnapshot(ctx context.Context, projectName string, volName string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// UpdateCustomVolumeSnapshot ...
func (b *mockBackend) UpdateCustomVolumeSnapshot(ctx context.Context, projectName string, volName string, newDesc string, newConfig map[string]string, newExpiryDate time.Time, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// RestoreCustomVolume ...
func (b *mockBackend) RestoreCustomVolume(ctx context.Context, projectName string, volName string, snapshotName string, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// BackupCustomVolume ...
func (b *mockBackend) BackupCustomVolume(projectName string, volName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// CreateCustomVolumeFromBackup ...
func (b *mockBackend) CreateCustomVolumeFromBackup(ctx context.Context, srcBackup backup.Info, srcData io.ReadSeeker, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// CreateCustomVolumeFromISO ...
func (b *mockBackend) CreateCustomVolumeFromISO(ctx context.Context, projectName string, volName string, srcData io.ReadSeeker, size int64, progressReporter ioprogress.ProgressReporter) error {
	return nil
}

// CreateCustomVolumeFromTarball ...
func (b *mockBackend) CreateCustomVolumeFromTarball(ctx context.Context, projectName string, volName string, srcData *os.File, progressReporter ioprogress.ProgressReporter) error {
	return nil
}
