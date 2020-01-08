package storage

import (
	"io"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

type mockBackend struct {
	name   string
	state  *state.State
	logger logger.Logger
}

func (b *mockBackend) ID() int64 {
	return -1
}

func (b *mockBackend) Name() string {
	return b.name
}

func (b *mockBackend) Driver() drivers.Driver {
	return nil
}

func (b *mockBackend) MigrationTypes(contentType drivers.ContentType, refresh bool) []migration.Type {
	return []migration.Type{
		{
			FSType:   migration.MigrationFSType_RSYNC,
			Features: []string{"xattrs", "delete", "compress", "bidirectional"},
		},
	}
}

func (b *mockBackend) GetResources() (*api.ResourcesStoragePool, error) {
	return nil, nil
}

func (b *mockBackend) Delete(localOnly bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) Update(localOnly bool, newDescription string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) Mount() (bool, error) {
	return true, nil
}

func (b *mockBackend) Unmount() (bool, error) {
	return true, nil
}

func (b *mockBackend) CreateInstance(inst instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceFromBackup(srcBackup backup.Info, srcData io.ReadSeeker, op *operations.Operation) (func(instance.Instance) error, func(), error) {
	return nil, nil, nil
}

func (b *mockBackend) CreateInstanceFromCopy(inst instance.Instance, src instance.Instance, snapshots bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceFromImage(inst instance.Instance, fingerprint string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceFromMigration(inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RenameInstance(inst instance.Instance, newName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteInstance(inst instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) UpdateInstance(inst instance.Instance, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) UpdateInstanceBackupFile(inst instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) MigrateInstance(inst instance.Instance, conn io.ReadWriteCloser, args migration.VolumeSourceArgs, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RefreshInstance(i instance.Instance, src instance.Instance, srcSnapshots []instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) BackupInstance(inst instance.Instance, targetPath string, optimized bool, snapshots bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) GetInstanceUsage(inst instance.Instance) (int64, error) {
	return 0, nil
}

func (b *mockBackend) SetInstanceQuota(inst instance.Instance, size string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) MountInstance(inst instance.Instance, op *operations.Operation) (bool, error) {
	return true, nil
}

func (b *mockBackend) UnmountInstance(inst instance.Instance, op *operations.Operation) (bool, error) {
	return true, nil
}

func (b *mockBackend) GetInstanceDisk(inst instance.Instance) (string, error) {
	return "", nil
}

func (b *mockBackend) CreateInstanceSnapshot(i instance.Instance, src instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RenameInstanceSnapshot(inst instance.Instance, newName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteInstanceSnapshot(inst instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RestoreInstanceSnapshot(inst instance.Instance, src instance.Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) MountInstanceSnapshot(inst instance.Instance, op *operations.Operation) (bool, error) {
	return true, nil
}

func (b *mockBackend) UnmountInstanceSnapshot(inst instance.Instance, op *operations.Operation) (bool, error) {
	return true, nil
}

func (b *mockBackend) UpdateInstanceSnapshot(inst instance.Instance, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) EnsureImage(fingerprint string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteImage(fingerprint string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) UpdateImage(fingerprint, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateCustomVolume(volName, desc string, config map[string]string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateCustomVolumeFromCopy(volName, desc string, config map[string]string, srcPoolName, srcVolName string, srcVolOnly bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RenameCustomVolume(volName string, newName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) UpdateCustomVolume(volName, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return ErrNotImplemented
}

func (b *mockBackend) DeleteCustomVolume(volName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) MigrateCustomVolume(conn io.ReadWriteCloser, args migration.VolumeSourceArgs, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateCustomVolumeFromMigration(conn io.ReadWriteCloser, args migration.VolumeTargetArgs, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) GetCustomVolumeUsage(volName string) (int64, error) {
	return 0, nil
}

func (b *mockBackend) MountCustomVolume(volName string, op *operations.Operation) (bool, error) {
	return true, nil
}

func (b *mockBackend) UnmountCustomVolume(volName string, op *operations.Operation) (bool, error) {
	return true, nil
}

func (b *mockBackend) CreateCustomVolumeSnapshot(volName string, newSnapshotName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RenameCustomVolumeSnapshot(volName string, newName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteCustomVolumeSnapshot(volName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) UpdateCustomVolumeSnapshot(volName, newDesc string, newConfig map[string]string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RestoreCustomVolume(volName string, snapshotName string, op *operations.Operation) error {
	return nil
}
