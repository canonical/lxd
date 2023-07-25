package storage

import (
	"io"
	"net/url"
	"time"

	"github.com/canonical/lxd/lxd/backup"
	backupConfig "github.com/canonical/lxd/lxd/backup/config"
	"github.com/canonical/lxd/lxd/cluster/request"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/revert"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/storage/s3/miniod"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/instancewriter"
	"github.com/canonical/lxd/shared/logger"
)

type mockBackend struct {
	name   string
	state  *state.State
	logger logger.Logger
	driver drivers.Driver
}

// ID returns the ID of the storage backend.
func (b *mockBackend) ID() int64 {
	return 1 //  The tests expect the storage pool ID to be 1.
}

// Name returns the name of the storage backend.
func (b *mockBackend) Name() string {
	return b.name
}

// Description returns an empty string (mock doesn't have a description)..
func (b *mockBackend) Description() string {
	return ""
}

// ValidateName doesn't validate the name in the mock backend.
func (b *mockBackend) ValidateName(value string) error {
	return nil
}

// Validate doesn't validate the configuration in the mock backend
func (b *mockBackend) Validate(config map[string]string) error {
	return nil
}

// Status returns the status of the storage network.
func (b *mockBackend) Status() string {
	return api.NetworkStatusUnknown
}

// LocalStatus returns the local status of the storage network.
func (b *mockBackend) LocalStatus() string {
	return api.NetworkStatusUnknown
}

// ToAPI returns an empty api.StoragePool object.
func (b *mockBackend) ToAPI() api.StoragePool {
	return api.StoragePool{}
}

// Driver returns the storage driver of the backend.
func (b *mockBackend) Driver() drivers.Driver {
	return b.driver
}

// MigrationTypes returns the types of migrations that can be done.
func (b *mockBackend) MigrationTypes(contentType drivers.ContentType, refresh bool, copySnapshots bool) []migration.Type {
	return []migration.Type{
		{
			FSType:   FallbackMigrationType(contentType),
			Features: []string{"xattrs", "delete", "compress", "bidirectional"},
		},
	}
}

// GetResources returns the resources of the storage pool.
func (b *mockBackend) GetResources() (*api.ResourcesStoragePool, error) {
	return nil, nil
}

// IsUsed checks if the storage backend is used.
func (b *mockBackend) IsUsed() (bool, error) {
	return false, nil
}

// Delete deletes the storage backend.
func (b *mockBackend) Delete(clientType request.ClientType, op *operations.Operation) error {
	return nil
}

// Update updates the storage backend.
func (b *mockBackend) Update(clientType request.ClientType, newDescription string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

// Create creates a new storage backend.
func (b *mockBackend) Create(clientType request.ClientType, op *operations.Operation) error {
	return nil
}

// Mount mounts the storage backend.
func (b *mockBackend) Mount() (bool, error) {
	return true, nil
}

// Unmount unmounts the storage backend.
func (b *mockBackend) Unmount() (bool, error) {
	return true, nil
}

// ApplyPatch applies a patch to the storage backend.
func (b *mockBackend) ApplyPatch(name string) error {
	return nil
}

// GetVolume retrieves a volume from the storage backend.
func (b *mockBackend) GetVolume(volType drivers.VolumeType, contentType drivers.ContentType, volName string, volConfig map[string]string) drivers.Volume {
	return drivers.Volume{}
}

// CreateInstance creates a new instance in the storage backend.
func (b *mockBackend) CreateInstance(inst instance.Instance, op *operations.Operation) error {
	return nil
}

// CreateInstanceFromBackup creates a new instance from a backup.
func (b *mockBackend) CreateInstanceFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (func(instance.Instance) error, revert.Hook, error) {
	return nil, nil, nil
}

// CreateInstanceFromCopy creates a new instance from a copy of an existing one.
func (b *mockBackend) CreateInstanceFromCopy(inst instance.Instance, src instance.Instance, snapshots bool, allowInconsistent bool, op *operations.Operation) error {
	return nil
}

// CreateInstanceFromImage creates a new instance from an image.
func (b *mockBackend) CreateInstanceFromImage(inst instance.Instance, fingerprint string, op *operations.Operation) error {
	return nil
}

// CreateInstanceFromMigration creates a new instance from a migration.
func (b *mockBackend) CreateInstanceFromMigration(inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	return nil
}

// RenameInstance renames an instance in the storage backend.
func (b *mockBackend) RenameInstance(inst instance.Instance, newName string, op *operations.Operation) error {
	return nil
}

// DeleteInstance deletes an instance from the storage backend.
func (b *mockBackend) DeleteInstance(inst instance.Instance, op *operations.Operation) error {
	return nil
}

// UpdateInstance updates an instance in the storage backend.
func (b *mockBackend) UpdateInstance(inst instance.Instance, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

// GenerateCustomVolumeBackupConfig generates a backup config for a custom volume.
func (b *mockBackend) GenerateCustomVolumeBackupConfig(projectName string, volName string, snapshots bool, op *operations.Operation) (*backupConfig.Config, error) {
	return nil, nil
}

// GenerateInstanceBackupConfig generates a backup config for an instance.
func (b *mockBackend) GenerateInstanceBackupConfig(inst instance.Instance, snapshots bool, op *operations.Operation) (*backupConfig.Config, error) {
	return nil, nil
}

// UpdateInstanceBackupFile updates the backup file of an instance.
func (b *mockBackend) UpdateInstanceBackupFile(inst instance.Instance, op *operations.Operation) error {
	return nil
}

// CheckInstanceBackupFileSnapshots checks the snapshots in an instance backup file.
func (b *mockBackend) CheckInstanceBackupFileSnapshots(backupConf *backupConfig.Config, projectName string, deleteMissing bool, op *operations.Operation) ([]*api.InstanceSnapshot, error) {
	return nil, nil
}

// ListUnknownVolumes lists volumes that aren't known by the storage backend.
func (b *mockBackend) ListUnknownVolumes(op *operations.Operation) (map[string][]*backupConfig.Config, error) {
	return nil, nil
}

// ImportInstance imports an instance to the storage backend.
func (b *mockBackend) ImportInstance(inst instance.Instance, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error) {
	return nil, nil
}

// MigrateInstance migrates an instance to another backend.
func (b *mockBackend) MigrateInstance(inst instance.Instance, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, op *operations.Operation) error {
	return nil
}

// CleanupInstancePaths cleans up the paths of an instance.
func (b *mockBackend) CleanupInstancePaths(inst instance.Instance, op *operations.Operation) error {
	return nil
}

// RefreshCustomVolume refreshes a custom volume in the storage backend.
func (b *mockBackend) RefreshCustomVolume(projectName string, srcProjectName string, volName string, desc string, config map[string]string, srcPoolName, srcVolName string, srcVolOnly bool, op *operations.Operation) error {
	return nil
}

// RefreshInstance refreshes an instance in the storage backend.
func (b *mockBackend) RefreshInstance(inst instance.Instance, src instance.Instance, srcSnapshots []instance.Instance, allowInconsistent bool, op *operations.Operation) error {
	return nil
}

// BackupInstance backs up an instance in the storage backend.
func (b *mockBackend) BackupInstance(inst instance.Instance, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, op *operations.Operation) error {
	return nil
}

// GetInstanceUsage gets the usage of an instance.
func (b *mockBackend) GetInstanceUsage(inst instance.Instance) (*VolumeUsage, error) {
	return nil, nil
}

// SetInstanceQuota sets the quota for an instance.
func (b *mockBackend) SetInstanceQuota(inst instance.Instance, size string, vmStateSize string, op *operations.Operation) error {
	return nil
}

// MountInstance mounts an instance.
func (b *mockBackend) MountInstance(inst instance.Instance, op *operations.Operation) (*MountInfo, error) {
	return &MountInfo{}, nil
}

// UnmountInstance unmounts an instance.
func (b *mockBackend) UnmountInstance(inst instance.Instance, op *operations.Operation) error {
	return nil
}

// CreateInstanceSnapshot creates a snapshot of an instance.
func (b *mockBackend) CreateInstanceSnapshot(i instance.Instance, src instance.Instance, op *operations.Operation) error {
	return nil
}

// RenameInstanceSnapshot renames a snapshot of an instance.
func (b *mockBackend) RenameInstanceSnapshot(inst instance.Instance, newName string, op *operations.Operation) error {
	return nil
}

// DeleteInstanceSnapshot deletes a snapshot of an instance.
func (b *mockBackend) DeleteInstanceSnapshot(inst instance.Instance, op *operations.Operation) error {
	return nil
}

// RestoreInstanceSnapshot restores a snapshot of an instance.
func (b *mockBackend) RestoreInstanceSnapshot(inst instance.Instance, src instance.Instance, op *operations.Operation) error {
	return nil
}

// MountInstanceSnapshot mounts a snapshot of an instance.
func (b *mockBackend) MountInstanceSnapshot(inst instance.Instance, op *operations.Operation) (*MountInfo, error) {
	return &MountInfo{}, nil
}

// UnmountInstanceSnapshot unmounts a snapshot of an instance.
func (b *mockBackend) UnmountInstanceSnapshot(inst instance.Instance, op *operations.Operation) error {
	return nil
}

// UpdateInstanceSnapshot updates a snapshot of an instance.
func (b *mockBackend) UpdateInstanceSnapshot(inst instance.Instance, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

// EnsureImage ensures an image is available in the storage backend.
func (b *mockBackend) EnsureImage(fingerprint string, op *operations.Operation) error {
	return nil
}

// DeleteImage deletes an image from the storage backend.
func (b *mockBackend) DeleteImage(fingerprint string, op *operations.Operation) error {
	return nil
}

// UpdateImage updates an image in the storage backend.
func (b *mockBackend) UpdateImage(fingerprint, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

// CreateBucket creates a new bucket in the storage backend.
func (b *mockBackend) CreateBucket(projectName string, bucket api.StorageBucketsPost, op *operations.Operation) error {
	return nil
}

// UpdateBucket updates a bucket in the storage backend.
func (b *mockBackend) UpdateBucket(projectName string, bucketName string, bucket api.StorageBucketPut, op *operations.Operation) error {
	return nil
}

// DeleteBucket deletes a bucket from the storage backend.
func (b *mockBackend) DeleteBucket(projectName string, bucketName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) ImportBucket(projectName string, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error) {
	return nil, nil
}

func (b *mockBackend) CreateBucketKey(projectName string, bucketName string, key api.StorageBucketKeysPost, op *operations.Operation) (*api.StorageBucketKey, error) {
	return nil, nil
}

// UpdateBucketKey updates a key for a bucket.
func (b *mockBackend) UpdateBucketKey(projectName string, bucketName string, keyName string, key api.StorageBucketKeyPut, op *operations.Operation) error {
	return nil
}

// DeleteBucketKey deletes a key from a bucket.
func (b *mockBackend) DeleteBucketKey(projectName string, bucketName string, keyName string, op *operations.Operation) error {
	return nil
}

// ActivateBucket activates a bucket in the storage backend.
func (b *mockBackend) ActivateBucket(projectName string, bucketName string, op *operations.Operation) (*miniod.Process, error) {
	return nil, nil
}

// GetBucketURL gets the URL of a bucket.
func (b *mockBackend) GetBucketURL(bucketName string) *url.URL {
	return nil
}

// CreateCustomVolume creates a custom volume in the storage backend.
func (b *mockBackend) CreateCustomVolume(projectName string, volName string, desc string, config map[string]string, contentType drivers.ContentType, op *operations.Operation) error {
	return nil
}

// CreateCustomVolumeFromCopy creates a custom volume from a copy of an existing one.
func (b *mockBackend) CreateCustomVolumeFromCopy(projectName string, srcProjectName string, volName string, desc string, config map[string]string, srcPoolName string, srcVolName string, srcVolOnly bool, op *operations.Operation) error {
	return nil
}

// RenameCustomVolume renames a custom volume in the storage backend.
func (b *mockBackend) RenameCustomVolume(projectName string, volName string, newName string, op *operations.Operation) error {
	return nil
}

// UpdateCustomVolume updates a custom volume in the storage backend.
func (b *mockBackend) UpdateCustomVolume(projectName string, volName string, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

// DeleteCustomVolume deletes a custom volume from the storage backend.
func (b *mockBackend) DeleteCustomVolume(projectName string, volName string, op *operations.Operation) error {
	return nil
}

// MigrateCustomVolume migrates a custom volume to another backend.
func (b *mockBackend) MigrateCustomVolume(projectName string, conn io.ReadWriteCloser, args *migration.VolumeSourceArgs, op *operations.Operation) error {
	return nil
}

// CreateCustomVolumeFromMigration creates a custom volume from a migration.
func (b *mockBackend) CreateCustomVolumeFromMigration(projectName string, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	return nil
}

// GetCustomVolumeDisk gets the disk of a custom volume.
func (b *mockBackend) GetCustomVolumeDisk(projectName string, volName string) (string, error) {
	return "", nil
}

// GetCustomVolumeUsage gets the usage of a custom volume.
func (b *mockBackend) GetCustomVolumeUsage(projectName string, volName string) (*VolumeUsage, error) {
	return nil, nil
}

func (b *mockBackend) MountCustomVolume(projectName string, volName string, op *operations.Operation) (*MountInfo, error) {
	return nil, nil
}

// UnmountCustomVolume unmounts a custom volume.
func (b *mockBackend) UnmountCustomVolume(projectName string, volName string, op *operations.Operation) (bool, error) {
	return true, nil
}

// ImportCustomVolume imports a custom volume to the storage backend.
func (b *mockBackend) ImportCustomVolume(projectName string, poolVol *backupConfig.Config, op *operations.Operation) (revert.Hook, error) {
	return nil, nil
}

// CreateCustomVolumeSnapshot creates a snapshot of a custom volume.
func (b *mockBackend) CreateCustomVolumeSnapshot(projectName string, volName string, newSnapshotName string, expiryDate time.Time, op *operations.Operation) error {
	return nil
}

// RenameCustomVolumeSnapshot renames a snapshot of a custom volume.
func (b *mockBackend) RenameCustomVolumeSnapshot(projectName string, volName string, newName string, op *operations.Operation) error {
	return nil
}

// DeleteCustomVolumeSnapshot deletes a snapshot of a custom volume.
func (b *mockBackend) DeleteCustomVolumeSnapshot(projectName string, volName string, op *operations.Operation) error {
	return nil
}

// UpdateCustomVolumeSnapshot updates a snapshot of a custom volume.
func (b *mockBackend) UpdateCustomVolumeSnapshot(projectName string, volName string, newDesc string, newConfig map[string]string, expiryDate time.Time, op *operations.Operation) error {
	return nil
}

// RestoreCustomVolume restores a custom volume from a snapshot.
func (b *mockBackend) RestoreCustomVolume(projectName string, volName string, snapshotName string, op *operations.Operation) error {
	return nil
}

// BackupCustomVolume backs up a custom volume in the storage backend.
func (b *mockBackend) BackupCustomVolume(projectName string, volName string, tarWriter *instancewriter.InstanceTarWriter, optimized bool, snapshots bool, op *operations.Operation) error {
	return nil
}

// CreateCustomVolumeFromBackup creates a custom volume from a backup.
func (b *mockBackend) CreateCustomVolumeFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) error {
	return nil
}

// CreateCustomVolumeFromISO creates a custom volume from an ISO.
func (b *mockBackend) CreateCustomVolumeFromISO(projectName string, volName string, srcData io.ReadSeeker, size int64, op *operations.Operation) error {
	return nil
}
