package storage

import (
	"io"

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

func (b *mockBackend) DaemonState() *state.State {
	return b.state
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

func (b *mockBackend) MigrationTypes(contentType drivers.ContentType) []migration.Type {
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

func (b *mockBackend) Delete(op *operations.Operation) error {
	return nil
}

func (b *mockBackend) Mount() (bool, error) {
	return true, nil
}

func (b *mockBackend) Unmount() (bool, error) {
	return true, nil
}

func (b *mockBackend) CreateInstance(i Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceFromBackup(i Instance, sourcePath string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceFromCopy(i Instance, src Instance, snapshots bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceFromImage(i Instance, fingerprint string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) CreateInstanceFromMigration(i Instance, conn io.ReadWriteCloser, args migration.SinkArgs, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RenameInstance(i Instance, newName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteInstance(i Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) MigrateInstance(i Instance, snapshots bool, args migration.SourceArgs) (migration.StorageSourceDriver, error) {
	return nil, nil
}

func (b *mockBackend) RefreshInstance(i Instance, src Instance, snapshots bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) BackupInstance(i Instance, targetPath string, optimized bool, snapshots bool, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) GetInstanceUsage(i Instance) (int64, error) {
	return 0, nil
}

func (b *mockBackend) SetInstanceQuota(i Instance, quota uint64) error {
	return nil
}

func (b *mockBackend) MountInstance(i Instance) (bool, error) {
	return true, nil
}

func (b *mockBackend) UnmountInstance(i Instance) (bool, error) {
	return true, nil
}

func (b *mockBackend) GetInstanceDisk(i Instance) (string, string, error) {
	return "", "", nil
}

func (b *mockBackend) CreateInstanceSnapshot(i Instance, name string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RenameInstanceSnapshot(i Instance, newName string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteInstanceSnapshot(i Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) RestoreInstanceSnapshot(i Instance, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) MountInstanceSnapshot(i Instance) (bool, error) {
	return true, nil
}

func (b *mockBackend) UnmountInstanceSnapshot(i Instance) (bool, error) {
	return true, nil
}

func (b *mockBackend) CreateImage(fingerprint string, op *operations.Operation) error {
	return nil
}

func (b *mockBackend) DeleteImage(fingerprint string, op *operations.Operation) error {
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

func (b *mockBackend) RestoreCustomVolume(volName string, snapshotName string, op *operations.Operation) error {
	return nil
}
