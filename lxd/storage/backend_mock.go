package storage

import (
	"io"
	"net/url"
	"time"

	"github.com/canonical/lxd/lxd/backup"
	backupConfig "github.com/canonical/lxd/lxd/backup/config"
	"github.com/canonical/lxd/lxd/cluster/request"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instancewriter"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/storage/s3/miniod"
	"github.com/canonical/lxd/shared/api"
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
func (b *mockBackend) Delete(clientType request.ClientType, op *operations.Operation) error {
	return nil
}

// Update ...
func (b *mockBackend) Update(clientType request.ClientType, newDescription string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

// Create ...
func (b *mockBackend) Create(clientType request.ClientType, op *operations.Operation) error {
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
func (b *mockBackend) CreateInstance(inst instance.Instance, op *operations.Operation) error {
	return nil
}

// CreateInstanceFromBackup ...
func (b *mockBackend) CreateInstanceFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (func(instance.Instance) error, revert.Hook, error) {
	return nil, nil, nil
}

// CreateInstanceFromCopy ...
func (b *mockBackend) CreateInstanceFromCopy(inst instance.Instance, src instance.Instance, snapshots bool, allowInconsistent bool, op *operations.Operation) error {
	return nil
}

// CreateInstanceFromImage ...
func (b *mockBackend) CreateInstanceFromImage(inst instance.Instance, fingerprint string, op *operations.Operation) error {
	return nil
}

// CreateInstanceFromMigration ...
func (b *mockBackend) CreateInstanceFromMigration(inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	return nil
}

// CreateInstanceFromConversion ...
func (b *mockBackend) CreateInstanceFromConversion(inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	return nil
}

// RenameInstance ...
func (b *mockBackend) RenameInstance(inst instance.Instance, newName string, op *operations.Operation) error {
	return nil
}

// DeleteInstance ...
func (b *mockBackend) DeleteInstance(inst instance.Instance, op *operations.Operation) error {
	return nil
}

// UpdateInstance ...
func (b *mockBackend) UpdateInstance(inst instance.Instance, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

// GenerateCustomVolumeBackupConfig ...
func (b *mockBackend) GenerateCustomVolumeBackupConfig(projectName string, volName string, snapshots bool, op *operations.Operation) (*backupConfig.Config, error) {
	return nil, nil
}

// GenerateInstanceBackupConfig ...
func (b *mockBackend) GenerateInstanceBackupConfig(inst instance.Instance, snapshots bool, op *operations.Operation) (*backupConfig.Config, error) {
	return nil, nil
}

// UpdateInstanceBackupFile ...
func (b *mockBackend) UpdateInstanceBackupFile(inst instance.Instance, snapshot bool, op *operations.Operation) error {
	return nil
}

// CheckInstanceBackupFileSnapshots checks the snapshots in storage against the given backup config.
func (b *mockBackend) CheckInstanceBackupFileSnapshots(backupConf *backupConfig.Config, projectName string, op *operations.Operation) ([]*api.InstanceSnapshot, error) {
	return nil, nil
}

// ListUnknownVolumes ...
func (b *mockBackend) ListUnknownVolumes(op *operations.Operation) (map[string][]*backupConfig.Config, error) {
	return nil, nil
}

// ImportInstance ...
func (b *mockBackend) ImportInstance(inst instance.Instance, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error) {
	return nil, nil
}

// MigrateInstance ...
func (b *mockBackend) MigrateInstance(inst instance.Instance, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, op *operations.Operation) error {
	return nil
}

// CleanupInstancePaths ...
func (b *mockBackend) CleanupInstancePaths(inst instance.Instance, op *operations.Operation) error {
	return nil
}

// RefreshCustomVolume ...
func (b *mockBackend) RefreshCustomVolume(projectName string, srcProjectName string, volName string, desc string, config map[string]string, srcPoolName, srcVolName string, srcVolOnly bool, op *operations.Operation) error {
	return nil
}

// RefreshInstance ...
func (b *mockBackend) RefreshInstance(inst instance.Instance, src instance.Instance, srcSnapshots []instance.Instance, allowInconsistent bool, op *operations.Operation) error {
	return nil
}

// BackupInstance ...
func (b *mockBackend) BackupInstance(inst instance.Instance, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, op *operations.Operation) error {
	return nil
}

// GetInstanceUsage ...
func (b *mockBackend) GetInstanceUsage(inst instance.Instance) (*VolumeUsage, error) {
	return nil, nil
}

// SetInstanceQuota ...
func (b *mockBackend) SetInstanceQuota(inst instance.Instance, size string, vmStateSize string, op *operations.Operation) error {
	return nil
}

// MountInstance ...
func (b *mockBackend) MountInstance(inst instance.Instance, op *operations.Operation) (*MountInfo, error) {
	return &MountInfo{}, nil
}

// UnmountInstance ...
func (b *mockBackend) UnmountInstance(inst instance.Instance, op *operations.Operation) error {
	return nil
}

// CreateInstanceSnapshot ...
func (b *mockBackend) CreateInstanceSnapshot(i instance.Instance, src instance.Instance, volumes instance.SnapshotVolumes, op *operations.Operation) error {
	return nil
}

// RenameInstanceSnapshot ...
func (b *mockBackend) RenameInstanceSnapshot(inst instance.Instance, newName string, op *operations.Operation) error {
	return nil
}

// DeleteInstanceSnapshot ...
func (b *mockBackend) DeleteInstanceSnapshot(inst instance.Instance, op *operations.Operation) error {
	return nil
}

// RestoreInstanceSnapshot ...
func (b *mockBackend) RestoreInstanceSnapshot(inst instance.Instance, src instance.Instance, volumes instance.RestoreVolumes, op *operations.Operation) error {
	return nil
}

// MountInstanceSnapshot ...
func (b *mockBackend) MountInstanceSnapshot(inst instance.Instance, op *operations.Operation) (*MountInfo, error) {
	return &MountInfo{}, nil
}

// UnmountInstanceSnapshot ...
func (b *mockBackend) UnmountInstanceSnapshot(inst instance.Instance, op *operations.Operation) error {
	return nil
}

// UpdateInstanceSnapshot ...
func (b *mockBackend) UpdateInstanceSnapshot(inst instance.Instance, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

// EnsureImage ...
func (b *mockBackend) EnsureImage(fingerprint string, op *operations.Operation) error {
	return nil
}

// DeleteImage ...
func (b *mockBackend) DeleteImage(fingerprint string, op *operations.Operation) error {
	return nil
}

// UpdateImage ...
func (b *mockBackend) UpdateImage(fingerprint, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

// CreateBucket ...
func (b *mockBackend) CreateBucket(projectName string, bucket api.StorageBucketsPost, op *operations.Operation) error {
	return nil
}

// UpdateBucket ...
func (b *mockBackend) UpdateBucket(projectName string, bucketName string, bucket api.StorageBucketPut, op *operations.Operation) error {
	return nil
}

// DeleteBucket ...
func (b *mockBackend) DeleteBucket(projectName string, bucketName string, op *operations.Operation) error {
	return nil
}

// ImportBucket ...
func (b *mockBackend) ImportBucket(projectName string, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error) {
	return nil, nil
}

// CreateBucketKey ...
func (b *mockBackend) CreateBucketKey(projectName string, bucketName string, key api.StorageBucketKeysPost, op *operations.Operation) (*api.StorageBucketKey, error) {
	return nil, nil
}

// UpdateBucketKey ...
func (b *mockBackend) UpdateBucketKey(projectName string, bucketName string, keyName string, key api.StorageBucketKeyPut, op *operations.Operation) error {
	return nil
}

// DeleteBucketKey ...
func (b *mockBackend) DeleteBucketKey(projectName string, bucketName string, keyName string, op *operations.Operation) error {
	return nil
}

// ActivateBucket ...
func (b *mockBackend) ActivateBucket(projectName string, bucketName string, op *operations.Operation) (*miniod.Process, error) {
	return nil, nil
}

// GetBucketURL ...
func (b *mockBackend) GetBucketURL(bucketName string) *url.URL {
	return nil
}

// CreateCustomVolume ...
func (b *mockBackend) CreateCustomVolume(projectName string, volName string, desc string, config map[string]string, contentType drivers.ContentType, op *operations.Operation) error {
	return nil
}

// CreateCustomVolumeFromCopy ...
func (b *mockBackend) CreateCustomVolumeFromCopy(projectName string, srcProjectName string, volName string, desc string, config map[string]string, srcPoolName string, srcVolName string, srcVolOnly bool, op *operations.Operation) error {
	return nil
}

// RenameCustomVolume ...
func (b *mockBackend) RenameCustomVolume(projectName string, volName string, newName string, op *operations.Operation) error {
	return nil
}

// UpdateCustomVolume ...
func (b *mockBackend) UpdateCustomVolume(projectName string, volName string, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

// DeleteCustomVolume ...
func (b *mockBackend) DeleteCustomVolume(projectName string, volName string, op *operations.Operation) error {
	return nil
}

// MigrateCustomVolume ...
func (b *mockBackend) MigrateCustomVolume(projectName string, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, op *operations.Operation) error {
	return nil
}

// CreateCustomVolumeFromMigration ...
func (b *mockBackend) CreateCustomVolumeFromMigration(projectName string, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	return nil
}

// GetCustomVolumeUsage ...
func (b *mockBackend) GetCustomVolumeUsage(projectName string, volName string) (*VolumeUsage, error) {
	return nil, nil
}

// MountCustomVolume ...
func (b *mockBackend) MountCustomVolume(projectName string, volName string, op *operations.Operation) (*MountInfo, error) {
	return nil, nil
}

// UnmountCustomVolume ...
func (b *mockBackend) UnmountCustomVolume(projectName string, volName string, op *operations.Operation) (bool, error) {
	return true, nil
}

// ImportCustomVolume ...
func (b *mockBackend) ImportCustomVolume(projectName string, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error) {
	return nil, nil
}

// CreateCustomVolumeSnapshot ...
func (b *mockBackend) CreateCustomVolumeSnapshot(projectName string, volName string, newSnapshotName string, newDescription string, newSnapshotUUID string, expiryDate time.Time, op *operations.Operation) error {
	return nil
}

// RenameCustomVolumeSnapshot ...
func (b *mockBackend) RenameCustomVolumeSnapshot(projectName string, volName string, newName string, op *operations.Operation) error {
	return nil
}

// DeleteCustomVolumeSnapshot ...
func (b *mockBackend) DeleteCustomVolumeSnapshot(projectName string, volName string, op *operations.Operation) error {
	return nil
}

// UpdateCustomVolumeSnapshot ...
func (b *mockBackend) UpdateCustomVolumeSnapshot(projectName string, volName string, newDesc string, newConfig map[string]string, expiryDate time.Time, op *operations.Operation) error {
	return nil
}

// RestoreCustomVolume ...
func (b *mockBackend) RestoreCustomVolume(projectName string, volName string, snapshotName string, op *operations.Operation) error {
	return nil
}

// BackupCustomVolume ...
func (b *mockBackend) BackupCustomVolume(projectName string, volName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, op *operations.Operation) error {
	return nil
}

// CreateCustomVolumeFromBackup ...
func (b *mockBackend) CreateCustomVolumeFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) error {
	return nil
}

// CreateCustomVolumeFromISO ...
func (b *mockBackend) CreateCustomVolumeFromISO(projectName string, volName string, srcData io.ReadSeeker, size int64, op *operations.Operation) error {
	return nil
}
